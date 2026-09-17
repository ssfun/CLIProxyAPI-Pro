package observability

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/pro/observability/internalusage"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/redisqueue"
	probackup "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/backup"
	log "github.com/sirupsen/logrus"
)

type Service struct {
	ctx              context.Context
	cfg              Config
	store            *Store
	server           *Server
	webDAVClient     *http.Client
	workers          sync.WaitGroup
	module           *Module
	backupUnregister func()
}

const webDAVRequestTimeout = 2 * time.Minute

func Start(ctx context.Context) (*Service, error) {
	return StartForPath(ctx, "")
}

func StartForPath(ctx context.Context, configFilePath string) (*Service, error) {
	cfg := LoadConfigForPath(configFilePath)
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	service := &Service{
		ctx:          ctx,
		cfg:          cfg,
		store:        store,
		webDAVClient: newWebDAVHTTPClient(),
		module:       New(),
	}
	service.backupUnregister = probackup.Default.RegisterLifecycle(probackup.Lifecycle{
		Pause: service.module.Pause, Resume: service.module.Resume,
	})
	service.server = NewServer(cfg, store)
	service.server.webDAVClient = service.webDAVClient
	if cfg.Enabled {
		redisqueue.SetEnabled(true)
		redisqueue.SetPersistenceQueueConfig(
			cfg.PersistenceQueueRetentionSeconds,
			cfg.PersistenceQueueMaxItems,
			cfg.PersistenceQueueMaxBytes,
		)
		redisqueue.SetPersistenceEnabled(true)
		redisqueue.SetUsageStatisticsEnabled(true)
		service.startWorker(func() { service.collect(ctx) })
		service.startWorker(func() { service.maintain(ctx) })
		service.startWorker(func() { service.runWebDAVBackups(ctx) })
		service.startWorker(func() { service.runModelPriceSync(ctx) })
	} else {
		redisqueue.SetPersistenceEnabled(false)
		redisqueue.SetEnabled(false)
		redisqueue.SetUsageStatisticsEnabled(false)
		log.Info("embedded usage collection disabled; shared Pro policy storage remains available")
	}
	go func() {
		<-ctx.Done()
		service.workers.Wait()
		if service.backupUnregister != nil {
			service.backupUnregister()
		}
		stopRuntimeStateWriter(service)
		if err := store.Close(); err != nil {
			log.WithError(err).Warn("failed to close embedded usage store")
		}
	}()

	log.Infof("shared Pro SQLite service started with db %s", cfg.DBPath)
	return service, nil
}

func (s *Service) startWorker(run func()) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		run()
	}()
}

func (s *Service) runModelPriceSync(ctx context.Context) {
	for {
		if err := s.module.Run(ctx, func() error {
			settings, err := s.store.GetMonitoringSettings(ctx)
			if err != nil {
				log.WithError(err).Warn("failed to load model price sync settings")
			} else if settings.ModelPriceSync.Enabled {
				state, stateErr := s.store.GetModelPriceSyncState(ctx)
				if stateErr != nil {
					log.WithError(stateErr).Warn("failed to load model price sync state")
				} else if lastRun := maxModelPriceSyncTimestamp(state.LastSuccess, state.LastAttempt); lastRun <= 0 || time.Since(time.UnixMilli(lastRun)) >= time.Duration(settings.ModelPriceSync.IntervalMinutes)*time.Minute {
					if _, syncErr := s.store.SyncModelsDevPrices(ctx, false, true); syncErr != nil {
						log.WithError(syncErr).Warn("failed to sync model prices from models.dev")
					}
				}
			}
			return nil
		}); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

func maxModelPriceSyncTimestamp(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (s *Service) Server() *Server {
	if s == nil {
		return nil
	}
	return s.server
}

func (s *Service) collect(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	pending := make([]internalusage.Event, 0, s.cfg.BatchSize)
	defer func() {
		if len(pending) == 0 {
			return
		}
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.store.InsertLiveEvents(flushCtx, pending); err != nil {
			log.WithError(err).Warn("failed to flush pending embedded usage events during shutdown")
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		didWork := len(pending) > 0
		err := s.module.Run(ctx, func() error {
			if len(pending) == 0 {
				items := redisqueue.PopOldestPersistence(s.cfg.BatchSize)
				didWork = len(items) > 0
				for _, item := range items {
					event, err := internalusage.NormalizeRaw(item)
					if err != nil {
						if addErr := s.store.AddDeadLetter(ctx, string(item), err); addErr != nil {
							log.WithError(addErr).Warn("failed to add embedded usage dead letter")
						}
						continue
					}
					pending = append(pending, event)
				}
			}
			if len(pending) == 0 {
				return nil
			}
			_, err := s.store.InsertLiveEvents(ctx, pending)
			if err == nil {
				pending = pending[:0]
			}
			return err
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.WithError(err).Warn("failed to insert embedded usage events")
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		if !didWork {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}

func (s *Service) maintain(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextMonitoringRetentionRun(time.Now()))):
		}
		if err := s.module.Run(ctx, func() error {
			if deleted, err := s.store.ApplyRetention(ctx, time.Now()); err != nil {
				log.WithError(err).Warn("failed to apply embedded usage retention")
			} else if deleted > 0 {
				log.Infof("embedded usage retention deleted %d events", deleted)
			}
			return nil
		}); err != nil {
			return
		}
	}
}

func nextMonitoringRetentionRun(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (s *Service) runWebDAVBackups(ctx context.Context) {
	for {
		if err := s.module.Run(ctx, func() error {
			settings, err := s.store.GetMonitoringSettings(ctx)
			if err != nil {
				log.WithError(err).Warn("failed to load monitoring settings")
			} else if lastBackup, historyErr := s.lastWebDAVBackupForTarget(ctx, settings.WebDAV); historyErr != nil {
				log.WithError(historyErr).Warn("failed to load persisted WebDAV backup history")
			} else if shouldRunWebDAVBackup(settings, lastBackup) {
				if err := s.backupToWebDAV(ctx, settings.WebDAV); err != nil {
					log.WithError(err).Warn("failed to backup embedded usage to WebDAV")
				}
			}
			return nil
		}); err != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

func webDAVTargetKey(cfg MonitoringWebDAVBackupConfig) string {
	normalizedURL := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if normalizedURL == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(normalizedURL))
	return fmt.Sprintf("%x", digest[:])
}

func (s *Service) lastWebDAVBackupForTarget(ctx context.Context, cfg MonitoringWebDAVBackupConfig) (time.Time, error) {
	operation, err := s.store.LastSuccessfulDataOperation(ctx, "backup")
	if err != nil || operation == nil || operation.FinishedAtMS <= 0 {
		return time.Time{}, err
	}
	targetKey, _ := operation.Metadata["targetKey"].(string)
	if targetKey == "" || targetKey != webDAVTargetKey(cfg) {
		return time.Time{}, nil
	}
	return time.UnixMilli(operation.FinishedAtMS), nil
}

func shouldRunWebDAVBackup(settings MonitoringSettings, lastBackup time.Time) bool {
	webdav := normalizeMonitoringSettings(settings).WebDAV
	if !webdav.Enabled || webdav.URL == "" {
		return false
	}
	if lastBackup.IsZero() {
		return true
	}
	return time.Since(lastBackup) >= time.Duration(webdav.IntervalMinutes)*time.Minute
}

func (s *Service) backupToWebDAV(ctx context.Context, cfg MonitoringWebDAVBackupConfig) error {
	cfg = normalizeMonitoringSettings(MonitoringSettings{WebDAV: cfg}).WebDAV
	if !cfg.Enabled || cfg.URL == "" {
		return nil
	}
	if s.server == nil {
		return fmt.Errorf("data management server is not available")
	}
	backup, err := s.server.backupToWebDAVWithConfigTrackedClient(ctx, cfg, "scheduled", s.webDAVHTTPClient())
	if err == nil {
		log.Infof("Pro data backup uploaded to WebDAV: %s", backup.FileName)
	}
	return err
}

func newWebDAVHTTPClient() *http.Client {
	return &http.Client{Timeout: webDAVRequestTimeout}
}

func webDAVContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, webDAVRequestTimeout)
}

func (s *Service) webDAVHTTPClient() *http.Client {
	if s != nil && s.webDAVClient != nil {
		return s.webDAVClient
	}
	return newWebDAVHTTPClient()
}

func setWebDAVAuth(req *http.Request, cfg MonitoringWebDAVBackupConfig) {
	if cfg.Username != "" || cfg.Password != "" {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
}

type webDAVMultistatus struct {
	Responses []webDAVResponse `xml:"response"`
}

type webDAVResponse struct {
	Href     string         `xml:"href"`
	Propstat webDAVPropstat `xml:"propstat"`
}

type webDAVPropstat struct {
	Prop webDAVProp `xml:"prop"`
}

type webDAVProp struct {
	ContentLength int64  `xml:"getcontentlength"`
	LastModified  string `xml:"getlastmodified"`
}

type WebDAVBackup struct {
	FileName       string `json:"fileName"`
	SizeBytes      int64  `json:"sizeBytes"`
	LastModified   string `json:"lastModified,omitempty"`
	LastModifiedMS int64  `json:"lastModifiedMs,omitempty"`
}

func listWebDAVBackups(ctx context.Context, client *http.Client, baseURL string, cfg MonitoringWebDAVBackupConfig) ([]WebDAVBackup, error) {
	if client == nil {
		client = newWebDAVHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", strings.TrimRight(baseURL, "/")+"/", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	setWebDAVAuth(req, cfg)
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("webdav propfind failed with status %d", response.StatusCode)
	}
	var listing webDAVMultistatus
	if err := xml.NewDecoder(response.Body).Decode(&listing); err != nil {
		return nil, err
	}
	backups := make([]WebDAVBackup, 0, len(listing.Responses))
	for _, item := range listing.Responses {
		fileName := path.Base(strings.TrimRight(item.Href, "/"))
		if !isKnownWebDAVBackupFileName(fileName) || path.Base(fileName) != fileName {
			continue
		}
		backup := WebDAVBackup{FileName: fileName, SizeBytes: item.Propstat.Prop.ContentLength, LastModified: strings.TrimSpace(item.Propstat.Prop.LastModified)}
		if modifiedAt, parseErr := http.ParseTime(backup.LastModified); parseErr == nil {
			backup.LastModifiedMS = modifiedAt.UnixMilli()
		}
		backups = append(backups, backup)
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].LastModifiedMS != backups[j].LastModifiedMS {
			return backups[i].LastModifiedMS > backups[j].LastModifiedMS
		}
		return backups[i].FileName > backups[j].FileName
	})
	return backups, nil
}

func pruneWebDAVBackups(ctx context.Context, client *http.Client, baseURL string, cfg MonitoringWebDAVBackupConfig, now time.Time) (int, error) {
	if client == nil {
		client = newWebDAVHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", baseURL+"/", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Depth", "1")
	setWebDAVAuth(req, cfg)
	response, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("webdav propfind failed with status %d", response.StatusCode)
	}
	var listing webDAVMultistatus
	if err := xml.NewDecoder(response.Body).Decode(&listing); err != nil {
		return 0, err
	}

	cutoff := now.AddDate(0, 0, -cfg.RetentionDays)
	deleted := 0
	for _, item := range listing.Responses {
		fileName := path.Base(strings.TrimRight(item.Href, "/"))
		if !isKnownWebDAVBackupFileName(fileName) {
			continue
		}
		modifiedAt, err := http.ParseTime(strings.TrimSpace(item.Propstat.Prop.LastModified))
		if err != nil || !modifiedAt.Before(cutoff) {
			continue
		}
		deleteReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+"/"+fileName, nil)
		if err != nil {
			return deleted, err
		}
		setWebDAVAuth(deleteReq, cfg)
		deleteResponse, err := client.Do(deleteReq)
		if err != nil {
			return deleted, err
		}
		deleteResponse.Body.Close()
		if deleteResponse.StatusCode < 200 || deleteResponse.StatusCode >= 300 {
			return deleted, fmt.Errorf("webdav delete failed for %s with status %d", fileName, deleteResponse.StatusCode)
		}
		deleted++
	}
	return deleted, nil
}

func isKnownWebDAVBackupFileName(fileName string) bool {
	if !strings.HasSuffix(fileName, ".jsonl") {
		return false
	}
	return strings.HasPrefix(fileName, "usage-export-") || strings.HasPrefix(fileName, "cliproxy-pro-backup-")
}
