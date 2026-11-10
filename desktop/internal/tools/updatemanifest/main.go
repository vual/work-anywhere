// Command updatemanifest generates, signs and verifies the release update
// manifest (docs/self-update/manifest.md).
//
//	updatemanifest keygen -key-id waw-release-2026 -out signing-key.txt
//	updatemanifest build -version 0.3.0 -setup <file> -portable <file> -apk <file> \
//	    -apk-version-code 87 -apk-cert-sha256 <hex> -out update-manifest.json
//	updatemanifest verify -in update-manifest.json -version 0.3.0
//	updatemanifest vectors -out docs/self-update/vectors.json
//
// build reads the base64 Ed25519 seed from WAW_UPDATE_SIGNING_KEY and the key
// id from WAW_UPDATE_SIGNING_KEY_ID.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/update/manifest"
)

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: updatemanifest keygen|build|verify|vectors [flags]"))
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "build":
		err = build(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "vectors":
		err = vectors(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "updatemanifest:", err)
	os.Exit(1)
}

func keygen(args []string) error {
	flags := flag.NewFlagSet("keygen", flag.ExitOnError)
	keyID := flags.String("key-id", "", "key id, e.g. waw-release-2026")
	out := flags.String("out", "", "file that receives the private seed (never commit it)")
	_ = flags.Parse(args)
	if *keyID == "" || *out == "" {
		return fmt.Errorf("-key-id and -out are required")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	seed := base64.StdEncoding.EncodeToString(private.Seed())
	if err := os.WriteFile(*out, []byte(seed+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("keyId:     %s\npublicKey: %s\nprivate seed written to %s; store it as WAW_UPDATE_SIGNING_KEY\n",
		*keyID, base64.RawURLEncoding.EncodeToString(public), *out)
	return nil
}

func signingKey() (string, ed25519.PrivateKey, error) {
	keyID := strings.TrimSpace(os.Getenv("WAW_UPDATE_SIGNING_KEY_ID"))
	encoded := strings.TrimSpace(os.Getenv("WAW_UPDATE_SIGNING_KEY"))
	if keyID == "" || encoded == "" {
		return "", nil, fmt.Errorf("WAW_UPDATE_SIGNING_KEY and WAW_UPDATE_SIGNING_KEY_ID must be set")
	}
	seed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(seed) != ed25519.SeedSize {
		return "", nil, fmt.Errorf("WAW_UPDATE_SIGNING_KEY must be a base64 32-byte seed")
	}
	return keyID, ed25519.NewKeyFromSeed(seed), nil
}

func describe(path, key, version string) (manifest.Asset, error) {
	file, err := os.Open(path)
	if err != nil {
		return manifest.Asset{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return manifest.Asset{}, err
	}
	name := filepath.Base(path)
	return manifest.Asset{
		Name:   name,
		URL:    manifest.ReleasesPrefix + "download/v" + version + "/" + name,
		Size:   size,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func build(args []string) error {
	flags := flag.NewFlagSet("build", flag.ExitOnError)
	version := flags.String("version", "", "release version without v")
	setup := flags.String("setup", "", "Windows installer path")
	portable := flags.String("portable", "", "Windows portable EXE path")
	apk := flags.String("apk", "", "Android APK path")
	versionCode := flags.Int64("apk-version-code", 0, "Android versionCode")
	certSHA := flags.String("apk-cert-sha256", "", "APK signing certificate SHA-256 (hex)")
	packageName := flags.String("apk-package", "io.workanywhere.work_anywhere", "Android package name")
	published := flags.String("published-at", "", "RFC 3339 time; defaults to now")
	out := flags.String("out", "update-manifest.json", "output path")
	_ = flags.Parse(args)
	if *version == "" || *setup == "" || *portable == "" || *apk == "" {
		return fmt.Errorf("-version, -setup, -portable and -apk are required")
	}
	keyID, key, err := signingKey()
	if err != nil {
		return err
	}
	publishedAt := *published
	if publishedAt == "" {
		publishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	payload := manifest.Payload{
		Channel:     manifest.ChannelStable,
		Version:     *version,
		Tag:         "v" + *version,
		PublishedAt: publishedAt,
		NotesURL:    manifest.ReleasesPrefix + "tag/v" + *version,
		Assets:      map[string]manifest.Asset{},
	}
	for key, path := range map[string]string{
		manifest.AssetWindowsSetup:    *setup,
		manifest.AssetWindowsPortable: *portable,
		manifest.AssetAndroidAPK:      *apk,
	} {
		asset, err := describe(path, key, *version)
		if err != nil {
			return err
		}
		if key == manifest.AssetAndroidAPK {
			asset.PackageName = *packageName
			asset.VersionCode = *versionCode
			asset.SigningCertSHA256 = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(*certSHA), ":", ""))
		}
		payload.Assets[key] = asset
	}
	if err := payload.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	envelope, err := manifest.Sign(raw, keyID, key)
	if err != nil {
		return err
	}
	// Self-check with the derived public key before writing.
	if _, err := manifest.Verify(envelope, map[string]ed25519.PublicKey{keyID: key.Public().(ed25519.PublicKey)}); err != nil {
		return fmt.Errorf("self-check failed: %w", err)
	}
	return os.WriteFile(*out, append(envelope, '\n'), 0o644)
}

func verify(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ExitOnError)
	in := flags.String("in", "update-manifest.json", "manifest path")
	version := flags.String("version", "", "expected version")
	_ = flags.Parse(args)
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	payload, err := manifest.Verify(raw, manifest.TrustedKeys())
	if err != nil {
		return err
	}
	if *version != "" && payload.Version != *version {
		return fmt.Errorf("manifest version %s, want %s", payload.Version, *version)
	}
	for _, key := range []string{manifest.AssetWindowsSetup, manifest.AssetWindowsPortable, manifest.AssetAndroidAPK} {
		if _, ok := payload.Assets[key]; !ok {
			return fmt.Errorf("manifest is missing %s", key)
		}
	}
	fmt.Printf("verified update manifest %s\n", payload.Version)
	return nil
}

func vectors(args []string) error {
	flags := flag.NewFlagSet("vectors", flag.ExitOnError)
	out := flags.String("out", "docs/self-update/vectors.json", "output path")
	_ = flags.Parse(args)
	raw, err := manifest.BuildVectors()
	if err != nil {
		return err
	}
	return os.WriteFile(*out, raw, 0o644)
}
