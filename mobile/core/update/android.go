package update

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// AndroidAssetKey is the manifest asset consumed by the Android app.
const AndroidAssetKey = "android-apk"

// AndroidRelease is the verified android-apk entry of a newer release.
type AndroidRelease struct {
	VersionName       string `json:"versionName"`
	VersionCode       int64  `json:"versionCode"`
	Size              int64  `json:"size"`
	NotesURL          string `json:"notesUrl"`
	PackageName       string `json:"packageName"`
	SigningCertSHA256 string `json:"signingCertSha256"`
	URL               string `json:"-"`
	SHA256            string `json:"-"`
}

// SelectAndroid applies the Android rules to a verified payload: the asset
// must exist, belong to packageName and carry a strictly greater versionCode.
// It returns nil when the installed build is current.
func SelectAndroid(payload *Payload, currentVersionCode int64, packageName, mirrorBase string) (*AndroidRelease, error) {
	asset, ok := payload.Assets[AndroidAssetKey]
	if !ok {
		return nil, NewError(CodeNoAsset, "the release has no Android APK")
	}
	if asset.PackageName != packageName {
		return nil, NewError(CodeManifestInvalid, "the Android asset belongs to another package")
	}
	if asset.VersionCode <= currentVersionCode {
		return nil, nil
	}
	url, err := AssetURL(asset.URL, mirrorBase)
	if err != nil {
		return nil, err
	}
	return &AndroidRelease{
		VersionName:       payload.Version,
		VersionCode:       asset.VersionCode,
		Size:              asset.Size,
		NotesURL:          payload.NotesURL,
		PackageName:       asset.PackageName,
		SigningCertSHA256: asset.SigningCertSHA256,
		URL:               url,
		SHA256:            asset.SHA256,
	}, nil
}

// CheckAndroid fetches the manifest (from the mirror when configured) and
// returns the newer Android release, or nil when up to date.
func CheckAndroid(ctx context.Context, client *http.Client, trusted map[string]ed25519.PublicKey, currentVersionCode int64, packageName, mirrorBase string) (*AndroidRelease, error) {
	manifestURL, err := ManifestURL(mirrorBase)
	if err != nil {
		return nil, err
	}
	payload, err := FetchManifest(ctx, client, manifestURL, trusted)
	if err != nil {
		return nil, err
	}
	return SelectAndroid(payload, currentVersionCode, packageName, mirrorBase)
}

// APKPaths returns the cache paths for one target versionCode.
func APKPaths(cacheDir string, versionCode int64) (dir, part, final string) {
	dir = filepath.Join(cacheDir, "updates", strconv.FormatInt(versionCode, 10))
	return dir, filepath.Join(dir, "app.apk.part"), filepath.Join(dir, "app.apk")
}

// DownloadAndroid downloads release into cacheDir/updates/<versionCode>/app.apk,
// removing other target versions first. It returns the verified APK path.
func DownloadAndroid(ctx context.Context, client *http.Client, release *AndroidRelease, cacheDir string, progress func(received, total int64)) (string, error) {
	dir, part, final := APKPaths(cacheDir, release.VersionCode)
	PruneAPKs(cacheDir, release.VersionCode)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", NewError(CodeDownloadFailed, err.Error())
	}
	if err := Download(ctx, client, release.URL, part, final, release.Size, release.SHA256, progress); err != nil {
		if ctx.Err() != nil {
			return "", NewError(CodeCancelled, "download cancelled")
		}
		return "", err
	}
	return final, nil
}

// PruneAPKs removes cached APKs for every versionCode except keep (0 removes all).
func PruneAPKs(cacheDir string, keep int64) {
	root := filepath.Join(cacheDir, "updates")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() == strconv.FormatInt(keep, 10) && keep > 0 {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, entry.Name()))
	}
}
