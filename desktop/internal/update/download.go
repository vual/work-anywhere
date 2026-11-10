package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/update/manifest"
)

const progressInterval = 500 * time.Millisecond

// NewHTTPClient returns a client that only follows HTTPS redirects.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if request.URL.Scheme != "https" {
				return errors.New("redirect to non-https URL refused")
			}
			return nil
		},
	}
}

// FetchManifest downloads and verifies the manifest.
func FetchManifest(ctx context.Context, client *http.Client, url string, trusted map[string]ed25519.PublicKey) (*manifest.Payload, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, NewError(CodeManifestUnavailable, err.Error())
	}
	request.Header.Set("Accept", "application/json, application/octet-stream")
	response, err := client.Do(request)
	if err != nil {
		return nil, NewError(CodeManifestUnavailable, err.Error())
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, NewError(CodeManifestUnavailable, "manifest request returned "+response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, manifest.MaxManifestBytes+1))
	if err != nil {
		return nil, NewError(CodeManifestUnavailable, err.Error())
	}
	return manifest.Verify(raw, trusted)
}

// Download fetches url into partPath (resuming when possible), verifies size
// and SHA-256 and renames it to finalPath.
func Download(ctx context.Context, client *http.Client, url, partPath, finalPath string, size int64, sha string, progress func(received, total int64)) error {
	if size <= 0 || size > manifest.MaxAssetBytes {
		return NewError(CodeManifestInvalid, "asset size is out of range")
	}
	if ok, _ := fileMatches(finalPath, size, sha); ok {
		return nil
	}
	_ = os.Remove(finalPath)
	offset := int64(0)
	if info, err := os.Stat(partPath); err == nil {
		if info.Size() <= size {
			offset = info.Size()
		} else {
			_ = os.Remove(partPath)
		}
	}
	if offset < size {
		if err := fetchRange(ctx, client, url, partPath, offset, size, progress); err != nil {
			return err
		}
	}
	ok, err := fileMatches(partPath, size, sha)
	if err != nil {
		return NewError(CodeDownloadFailed, err.Error())
	}
	if !ok {
		_ = os.Remove(partPath)
		return NewError(CodeHashMismatch, "downloaded file does not match the manifest")
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		return NewError(CodeDownloadFailed, err.Error())
	}
	return nil
}

func fetchRange(ctx context.Context, client *http.Client, url, partPath string, offset, size int64, progress func(received, total int64)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return NewError(CodeDownloadFailed, err.Error())
	}
	if offset > 0 {
		request.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return NewError(CodeDownloadFailed, err.Error())
	}
	defer response.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case offset > 0 && response.StatusCode == http.StatusPartialContent:
		flags |= os.O_APPEND
	case response.StatusCode == http.StatusOK:
		offset = 0
		flags |= os.O_TRUNC
	case response.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(partPath)
		return NewError(CodeDownloadFailed, "server rejected the resume range; retry")
	default:
		return NewError(CodeDownloadFailed, "download returned "+response.Status)
	}
	file, err := os.OpenFile(partPath, flags, 0o600)
	if err != nil {
		return NewError(CodeDownloadFailed, err.Error())
	}
	received := offset
	last := time.Time{}
	buffer := make([]byte, 64<<10)
	reader := io.LimitReader(response.Body, size-offset+1)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if received+int64(n) > size {
				file.Close()
				_ = os.Remove(partPath)
				return NewError(CodeHashMismatch, "download is larger than the manifest size")
			}
			if _, err := file.Write(buffer[:n]); err != nil {
				file.Close()
				return NewError(CodeDownloadFailed, err.Error())
			}
			received += int64(n)
			if progress != nil && time.Since(last) >= progressInterval {
				last = time.Now()
				progress(received, size)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return NewError(CodeDownloadFailed, readErr.Error())
		}
	}
	if err := file.Close(); err != nil {
		return NewError(CodeDownloadFailed, err.Error())
	}
	if progress != nil {
		progress(received, size)
	}
	if received != size {
		return NewError(CodeDownloadFailed, fmt.Sprintf("download ended at %d of %d bytes", received, size))
	}
	return nil
}

// fileMatches reports whether path has the expected size and SHA-256.
func fileMatches(path string, size int64, sha string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != size {
		return false, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, err
	}
	return hex.EncodeToString(hash.Sum(nil)) == sha, nil
}
