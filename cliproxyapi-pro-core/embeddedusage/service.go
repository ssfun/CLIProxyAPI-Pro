package embeddedusage

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage/internalusage"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/redisqueue"
	log "github.com/sirupsen/logrus"
)

type Service struct {
	ctx     context.Context
	cfg     Config
	store   *Store
	server  *Server
	workers sync.WaitGroup
}

func Start(ctx context.Context) (*Service, error) {
	cfg := LoadConfig()
	if !cfg.Enabled {
		log.Info("embedded usage service disabled")
		return nil, nil
	}

	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	redisqueue.SetEnabled(true)
	redisqueue.SetUsageStatisticsEnabled(true)

	service := &Service{
		ctx:   ctx,
		cfg:   cfg,
		store: store,
	}
	service.server = NewServer(cfg, store)
	service.startWorker(func() { service.collect(ctx) })
	service.startWorker(func() { service.maintain(ctx) })
	service.startWorker(func() { service.runWebDAVBackups(ctx) })
	service.startWorker(func() { service.runModelPriceSync(ctx) })
	go func() {
		<-ctx.Done()
		service.workers.Wait()
		stopRuntimeStateWriter(service)
		if err := store.Close(); err != nil {
			log.WithError(err).Warn("failed to close embedded usage store")
		}
	}()

	log.Infof("embedded usage service started with db %s", cfg.DBPath)
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

		if len(pending) == 0 {
			items := redisqueue.PopOldest(s.cfg.BatchSize)
			if len(items) == 0 {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					continue
				}
			}
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
			continue
		}
		if _, err := s.store.InsertLiveEvents(ctx, pending); err != nil {
			log.WithError(err).Warn("failed to insert embedded usage events")
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		pending = pending[:0]
	}
}

func (s *Service) maintain(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextMonitoringRetentionRun(time.Now()))):
		}
		if deleted, err := s.store.ApplyRetention(ctx, time.Now()); err != nil {
			log.WithError(err).Warn("failed to apply embedded usage retention")
		} else if deleted > 0 {
			log.Infof("embedded usage retention deleted %d events", deleted)
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
	var lastBackup time.Time
	for {
		settings, err := s.store.GetMonitoringSettings(ctx)
		if err != nil {
			log.WithError(err).Warn("failed to load monitoring settings")
		} else if shouldRunWebDAVBackup(settings, lastBackup) {
			if err := s.backupToWebDAV(ctx, settings.WebDAV); err != nil {
				log.WithError(err).Warn("failed to backup embedded usage to WebDAV")
			} else {
				lastBackup = time.Now()
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
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
	data, err := s.server.exportJSONL(ctx)
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(cfg.URL, "/")
	url := baseURL + fmt.Sprintf("/usage-export-%s.jsonl", time.Now().UTC().Format("20060102_150405"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	setWebDAVAuth(req, cfg)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webdav upload failed with status %d", response.StatusCode)
	}
	log.Infof("embedded usage backup uploaded to WebDAV: %s", url)
	if cfg.RetentionDays > 0 {
		if deleted, err := pruneWebDAVBackups(ctx, baseURL, cfg, time.Now().UTC()); err != nil {
			log.WithError(err).Warn("failed to prune embedded usage WebDAV backups")
		} else if deleted > 0 {
			log.Infof("embedded usage WebDAV retention deleted %d backups", deleted)
		}
	}
	return nil
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
	LastModified string `xml:"getlastmodified"`
}

func pruneWebDAVBackups(ctx context.Context, baseURL string, cfg MonitoringWebDAVBackupConfig, now time.Time) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", baseURL+"/", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Depth", "1")
	setWebDAVAuth(req, cfg)
	response, err := http.DefaultClient.Do(req)
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
		if !strings.HasPrefix(fileName, "usage-export-") || !strings.HasSuffix(fileName, ".jsonl") {
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
		deleteResponse, err := http.DefaultClient.Do(deleteReq)
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
