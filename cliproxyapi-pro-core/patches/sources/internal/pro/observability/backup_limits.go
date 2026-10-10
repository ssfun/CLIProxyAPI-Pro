package observability

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

const defaultBackupMaxBytes = 256 * 1024 * 1024

// Use the same byte budget for upload, WebDAV, preview, and legacy import.
// A zero-value Config also retains a finite limit for embedded callers.
func (s *Server) backupReader(body io.ReadCloser, contentLength int64) (io.ReadCloser, error) {
	limit := s.cfg.BackupMaxBytes
	if limit <= 0 {
		limit = defaultBackupMaxBytes
	}
	if contentLength > limit {
		return nil, &http.MaxBytesError{Limit: limit}
	}
	// MaxBytesReader also bounds chunked/unknown-length bodies without limit+1
	// arithmetic, which would overflow for the largest valid configuration.
	return http.MaxBytesReader(nil, body, limit), nil
}

func (s *Server) readBackup(body io.ReadCloser, contentLength int64) ([]byte, error) {
	reader, err := s.backupReader(body, contentLength)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

func writeBackupReadError(c *gin.Context, err error, fallbackStatus int) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf(
			"backup exceeds restore/preview limit of %d bytes; increase PRO_BACKUP_MAX_BYTES and restart the service", tooLarge.Limit)})
		return
	}
	c.JSON(fallbackStatus, gin.H{"error": err.Error()})
}
