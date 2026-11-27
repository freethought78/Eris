package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Updating the server from Eris's GitHub releases. Each release carries
// eris-server-<os>-<arch>[.exe] and SHA256SUMS; `eris-server -update`
// downloads the one for this machine, checks it against SHA256SUMS, and puts
// it in place of the running program (which keeps running until restarted:
// apps reconnect by themselves when it comes back).
//
// Release builds also do this by themselves (autoUpdate): they check every
// hour, download and check a newer release, and restart into it as soon as
// nobody is streaming. Members' apps reconnect, and suggest updating the app
// if it's now older than the server.

const releasesAPI = "https://api.github.com/repos/freethought78/Eris/releases/latest"

// devBuild: built from the source folder (not a release). It never installs
// a release over itself, unless pointed at a test release page.
func devBuild() bool {
	return strings.HasSuffix(version, "-dev") && os.Getenv("ERIS_RELEASES_API") == ""
}

// releasesURL is where updates are looked for; ERIS_RELEASES_API points it
// elsewhere, for testing.
func releasesURL() string {
	if u := os.Getenv("ERIS_RELEASES_API"); u != "" {
		return u
	}
	return releasesAPI
}

// program is this program's path, found at start-up: once an update has
// renamed the running file to .old, asking again (/proc/self/exe on Linux)
// would give the .old one.
var program string

// thisProgram is the running program's real path.
func thisProgram() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// found is a newer release put in place of this program; now means it was
// asked for (restart straight away, not when nobody is streaming).
type found struct {
	version string
	now     bool
}

// autoUpdate checks for a newer release every `every` (periodic false: only
// when asked) and whenever something arrives on ask. With install, or when
// asked, it puts it in place of this program and sends it on ready (once);
// otherwise, or when the program can't be replaced (read-only, say), it only
// says there's one.
func autoUpdate(ctx context.Context, every time.Duration, periodic, install bool, ask <-chan struct{}, ready chan<- found) {
	told := ""
	asked := false
	if !periodic {
		select {
		case <-ctx.Done():
			return
		case <-ask:
			asked = true
		}
	}
	for {
		if asked {
			log.Printf("update: checking for a newer eris-server now (this is %s)", version)
		}
		setUpdate(func(u *updateInfo) { u.State, u.Error = "checking", "" })
		cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		r, err := latest(cctx, releasesURL())
		now := time.Now().UnixMilli()
		switch {
		case err != nil:
			setUpdate(func(u *updateInfo) { u.State, u.Error, u.Checked = "error", err.Error(), now })
			if asked {
				log.Printf("update: couldn't check: %v", err)
			}
		case !newer(r.version(), version):
			setUpdate(func(u *updateInfo) { u.State, u.Latest, u.Checked = "latest", r.version(), now })
			if asked {
				log.Printf("update: eris-server %s is the latest", version)
			}
		case devBuild():
			// A dev build is the code being worked on: it says a release is
			// out, but never replaces itself with it.
			setUpdate(func(u *updateInfo) { u.State, u.Latest, u.Checked = "available", r.version(), now })
			if asked || told != r.version() {
				log.Printf("update: eris-server %s is out; this is a dev build, which doesn't install releases", r.version())
			}
			told = r.version()
		case install || asked:
			var perr error
			if program == "" {
				perr = errors.New("this program's path isn't known")
			} else {
				var v string
				if v, perr = selfUpdate(cctx, releasesURL(), program); perr == nil && v != "" {
					cancel()
					setUpdate(func(u *updateInfo) { u.State, u.Latest, u.Checked = "downloaded", v, now })
					if asked {
						log.Printf("update: downloaded eris-server %s; restarting into it now", v)
					} else {
						log.Printf("update: downloaded eris-server %s; restarting into it as soon as nobody is streaming", v)
					}
					ready <- found{v, asked}
					return
				}
			}
			if perr == nil {
				perr = errors.New("nothing was installed")
			}
			setUpdate(func(u *updateInfo) { u.State, u.Latest, u.Checked, u.Error = "error", r.version(), now, perr.Error() })
			if asked || told != r.version() {
				log.Printf("update: eris-server %s is out, but couldn't be installed: %v", r.version(), perr)
			}
			told = r.version()
		default:
			setUpdate(func(u *updateInfo) { u.State, u.Latest, u.Checked = "available", r.version(), now })
			if told != r.version() {
				log.Printf("update: eris-server %s is available (this is %s); type update here, use the dashboard's Check for updates, or run eris-server -update", r.version(), version)
			}
			told = r.version()
		}
		cancel()
		asked = false
		var wait <-chan time.Time
		if periodic {
			wait = time.After(every)
		}
		select {
		case <-ctx.Done():
			return
		case <-wait:
		case <-ask:
			asked = true
		}
	}
}

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) version() string { return strings.TrimPrefix(r.Tag, "v") }

func (r release) asset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func assetName() string {
	name := fmt.Sprintf("eris-server-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

var updateClient = &http.Client{Timeout: 5 * time.Minute}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "eris-server/"+version)
	resp, err := updateClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}

func latest(ctx context.Context, api string) (release, error) {
	var r release
	resp, err := get(ctx, api)
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	return r, json.NewDecoder(resp.Body).Decode(&r)
}

// newer says whether version a is newer than b ("0.2.0" > "0.1.9" >
// "0.1.0" > "0.1.0-dev").
func newer(a, b string) bool {
	pa, sa := splitVersion(a)
	pb, sb := splitVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return sa == "" && sb != "" // a release is newer than its pre-release
}

func splitVersion(v string) ([3]int, string) {
	var out [3]int
	v, suffix, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	for i, part := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(part)
	}
	return out, suffix
}

// checkForUpdate logs when a newer server is out (at start-up, then daily).

// selfUpdate replaces the program at exe with the latest release's, if newer.
func selfUpdate(ctx context.Context, api, exe string) (string, error) {
	r, err := latest(ctx, api)
	if err != nil {
		return "", fmt.Errorf("checking for updates: %w", err)
	}
	if !newer(r.version(), version) {
		return "", nil
	}
	binURL, sumsURL := r.asset(assetName()), r.asset("SHA256SUMS")
	if binURL == "" || sumsURL == "" {
		return "", fmt.Errorf("release %s has no %s or SHA256SUMS", r.Tag, assetName())
	}
	want, err := checksumFor(ctx, sumsURL, assetName())
	if err != nil {
		return "", err
	}
	tmp := exe + ".new"
	if err := download(ctx, binURL, tmp, want); err != nil {
		os.Remove(tmp)
		return "", err
	}
	// The running program can be renamed (also on Windows), so the old one
	// is kept beside the new one until the next update.
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(old, exe)
		return "", err
	}
	return r.version(), nil
}

func checksumFor(ctx context.Context, url, name string) (string, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no line for %s", name)
}

func download(ctx context.Context, url, path, wantSHA string) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return errors.New("the download doesn't match SHA256SUMS; nothing was changed")
	}
	return nil
}

// runUpdate is `eris-server -update`.
func runUpdate() {
	exe, err := thisProgram()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	v, err := selfUpdate(ctx, releasesURL(), exe)
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "update failed:", err)
		os.Exit(1)
	case v == "":
		fmt.Printf("eris-server %s is the latest.\n", version)
	default:
		fmt.Printf("Updated eris-server %s -> %s. Restart it to run the new version (apps reconnect by themselves).\n", version, v)
	}
}
