package supervisor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const dockerAPI = "/v1.43"

var errDockerNotModified = errors.New("container already running")

// DockerRuntime talks to the engine over the socket. The core container does
// not get this client.
type DockerRuntime struct {
	Client  *http.Client
	BaseURL string
	Network string
	Env     []string
	Binds   []string
}

// CoreEnv is the environment the Compose core service receives. Replacements
// started by the supervisor must carry the same files and supervisor address.
// CoreEnv is the process environment shared by Compose and by containers
// this supervisor creates. Secrets stay in the mounted files. The core image
// entrypoint reads those files into the bootstrap contract. TOGAVSU_DOMAIN,
// when the supervisor has it, is passed through so the replacement matches
// the installed domain file.
func CoreEnv() []string {
	env := []string{"SUPERVISOR_URL=http://supervisor:8081"}
	if domain := strings.TrimSpace(os.Getenv("TOGAVSU_DOMAIN")); domain != "" {
		env = append(env, "TOGAVSU_DOMAIN="+domain)
	}
	return env
}

// CoreBinds mounts the installer secret volume. The default name matches the
// Compose project name togavsu.
func CoreBinds(volume string) []string {
	if volume == "" {
		volume = "togavsu_appsecrets"
	}
	return []string{volume + ":/appsecrets"}
}

func (d *DockerRuntime) coreEnv() []string {
	if len(d.Env) == 0 {
		return CoreEnv()
	}
	return d.Env
}

func (d *DockerRuntime) coreBinds() []string {
	if len(d.Binds) == 0 {
		return CoreBinds("")
	}
	return d.Binds
}

func DockerSocketClient(socketPath string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}}
}

func (d *DockerRuntime) base() string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	return "http://docker"
}

func (d *DockerRuntime) ListCores(ctx context.Context) ([]ContainerInfo, error) {
	return d.list(ctx, map[string][]string{"label": {"togavsu.role=core"}}, true)
}

func (d *DockerRuntime) ListAllCores(ctx context.Context) ([]ContainerInfo, error) {
	return d.list(ctx, map[string][]string{"label": {"togavsu.role=core"}}, false)
}

func (d *DockerRuntime) list(ctx context.Context, filters map[string][]string, runningOnly bool) ([]ContainerInfo, error) {
	raw, err := json.Marshal(filters)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("all", "1")
	q.Set("filters", string(raw))
	var listed []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		State  string            `json:"State"`
	}
	if err := d.call(ctx, http.MethodGet, dockerAPI+"/containers/json?"+q.Encode(), nil, &listed); err != nil {
		return nil, err
	}
	var out []ContainerInfo
	for _, item := range listed {
		if runningOnly && item.State != "" && item.State != "running" {
			continue
		}
		name := ""
		if len(item.Names) > 0 {
			name = strings.TrimPrefix(item.Names[0], "/")
		}
		state := item.State
		running := state == "" || state == "running"
		out = append(out, ContainerInfo{ID: item.ID, Name: name, Image: item.Image, Labels: item.Labels, State: state, Running: running})
	}
	return out, nil
}

func (d *DockerRuntime) Pull(ctx context.Context, image string) error {
	if err := ImmutableImage(image); err != nil {
		return err
	}
	q := url.Values{}
	q.Set("fromImage", image)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base()+dockerAPI+"/images/create?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("docker pull %s: %s", res.Status, strings.TrimSpace(string(msg)))
	}
	return readPullStream(res.Body)
}

func readPullStream(r io.Reader) error {
	dec := json.NewDecoder(r)
	saw := false
	for {
		var msg struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		err := dec.Decode(&msg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("pull stream ended early: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("pull failed: %s", msg.Error)
		}
		if msg.Status == "" {
			return fmt.Errorf("pull stream ended early: empty progress")
		}
		saw = true
	}
	if !saw {
		return fmt.Errorf("pull stream ended early")
	}
	return nil
}

func (d *DockerRuntime) Start(ctx context.Context, id string) error {
	err := d.call(ctx, http.MethodPost, dockerAPI+"/containers/"+id+"/start", nil, nil)
	if errors.Is(err, errDockerNotModified) {
		return nil
	}
	return err
}

func (d *DockerRuntime) Inspect(ctx context.Context, id string) (ContainerInfo, error) {
	var inspected struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		State struct {
			Status  string `json:"Status"`
			Running bool   `json:"Running"`
		} `json:"State"`
	}
	if err := d.call(ctx, http.MethodGet, dockerAPI+"/containers/"+id+"/json", nil, &inspected); err != nil {
		return ContainerInfo{}, err
	}
	state := inspected.State.Status
	running := inspected.State.Running || state == "running"
	return ContainerInfo{ID: id, Name: strings.TrimPrefix(inspected.Name, "/"), State: state, Running: running}, nil
}

func (d *DockerRuntime) StartCore(ctx context.Context, image, name string) (ContainerInfo, error) {
	body := map[string]any{
		"Image": image,
		"User":  "65532:65532",
		"Labels": map[string]string{
			"togavsu.role":        "core",
			"togavsu.core.active": "0",
		},
		"Env": d.coreEnv(),
		"HostConfig": map[string]any{
			"NetworkMode": d.Network,
			"Binds":       d.coreBinds(),
			"RestartPolicy": map[string]any{
				"Name": "unless-stopped",
			},
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	path := dockerAPI + "/containers/create?name=" + url.QueryEscape(name)
	if err := d.call(ctx, http.MethodPost, path, body, &created); err != nil {
		return ContainerInfo{}, err
	}
	if err := d.call(ctx, http.MethodPost, dockerAPI+"/containers/"+created.ID+"/start", nil, nil); err != nil {
		return ContainerInfo{}, err
	}
	return ContainerInfo{ID: created.ID, Name: name, Image: image}, nil
}

func (d *DockerRuntime) WaitHealthy(ctx context.Context, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var inspected struct {
			State struct {
				Status string `json:"Status"`
				Health *struct {
					Status string `json:"Status"`
				} `json:"Health"`
			} `json:"State"`
		}
		if err := d.call(ctx, http.MethodGet, dockerAPI+"/containers/"+id+"/json", nil, &inspected); err != nil {
			return err
		}
		if inspected.State.Health == nil || inspected.State.Health.Status == "" || inspected.State.Health.Status == "none" {
			return fmt.Errorf("%w: core image has no health check", ErrHealth)
		}
		switch inspected.State.Health.Status {
		case "healthy":
			return nil
		case "unhealthy":
			return fmt.Errorf("%w: container reported unhealthy", ErrHealth)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: timed out after %s", ErrHealth, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (d *DockerRuntime) Stop(ctx context.Context, id string) error {
	return d.call(ctx, http.MethodPost, dockerAPI+"/containers/"+id+"/stop?t=20", nil, nil)
}

func (d *DockerRuntime) Remove(ctx context.Context, id string) error {
	return d.call(ctx, http.MethodDelete, dockerAPI+"/containers/"+id+"?force=1", nil, nil)
}

func (d *DockerRuntime) StartModule(ctx context.Context, name string) error {
	return d.module(ctx, name, "start")
}

func (d *DockerRuntime) StopModule(ctx context.Context, name string) error {
	return d.module(ctx, name, "stop")
}

func (d *DockerRuntime) module(ctx context.Context, name, action string) error {
	if err := safeModule(name); err != nil {
		return err
	}
	found, err := d.list(ctx, map[string][]string{"label": {"togavsu.module=" + name}}, false)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return ErrNotFound
	}
	return d.call(ctx, http.MethodPost, dockerAPI+"/containers/"+found[0].ID+"/"+action, nil, nil)
}

func safeModule(name string) error {
	if name == "" || len(name) > 63 {
		return ErrBadRequest
	}
	for i, c := range name {
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || (c == '-' && i > 0)
		if !ok {
			return ErrBadRequest
		}
	}
	return nil
}

func (d *DockerRuntime) call(ctx context.Context, method, path string, body any, dest any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.base()+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		_, _ = io.Copy(io.Discard, res.Body)
		return errDockerNotModified
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		if res.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("docker %s: %s", res.Status, strings.TrimSpace(string(msg)))
	}
	if dest == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(dest)
}

// DockerMigrator runs `migrate` in a short-lived container of the new image.
// The image must print migrated=true when it applied a migration.
type DockerMigrator struct {
	Docker      *DockerRuntime
	DatabaseURL string
}

func (m DockerMigrator) Apply(ctx context.Context, image string) (bool, error) {
	if m.Docker == nil || m.Docker.Client == nil {
		return false, ErrMigratorUnconfigured
	}
	if err := m.Docker.Pull(ctx, image); err != nil {
		return false, err
	}
	env := append([]string{}, m.Docker.coreEnv()...)
	body := map[string]any{
		"Image": image,
		"User":  "65532:65532",
		"Cmd":   []string{"migrate"},
		"Env":   env,
		"Tty":   true,
		"HostConfig": map[string]any{
			"NetworkMode": m.Docker.Network,
			"Binds":       m.Docker.coreBinds(),
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	suffix, err := newBackupID(time.Now())
	if err != nil {
		return false, err
	}
	name := "togavsu-migrate-" + suffix[len(suffix)-8:]
	if err := m.Docker.call(ctx, http.MethodPost, dockerAPI+"/containers/create?name="+name, body, &created); err != nil {
		return false, err
	}
	defer m.Docker.call(ctx, http.MethodDelete, dockerAPI+"/containers/"+created.ID+"?force=1", nil, nil)
	if err := m.Docker.call(ctx, http.MethodPost, dockerAPI+"/containers/"+created.ID+"/start", nil, nil); err != nil {
		return false, err
	}
	var waited struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := m.Docker.call(ctx, http.MethodPost, dockerAPI+"/containers/"+created.ID+"/wait", nil, &waited); err != nil {
		return false, err
	}
	logs, err := m.logs(ctx, created.ID)
	if err != nil {
		return false, err
	}
	ran := strings.Contains(logs, "migrated=true")
	if waited.StatusCode != 0 {
		return ran, fmt.Errorf("migrate exited %d", waited.StatusCode)
	}
	return ran, nil
}

func (m DockerMigrator) logs(ctx context.Context, id string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Docker.base()+dockerAPI+"/containers/"+id+"/logs?stdout=1&stderr=1", nil)
	if err != nil {
		return "", err
	}
	res, err := m.Docker.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("docker logs %s", res.Status)
	}
	return string(raw), nil
}

// ReadDockerLog demuxes the non-TTY docker log stream.
func ReadDockerLog(r io.Reader) (string, error) {
	var out strings.Builder
	for {
		var hdr [8]byte
		_, err := io.ReadFull(r, hdr[:])
		if err == io.EOF {
			return out.String(), nil
		}
		if err != nil {
			return "", err
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		if n > 1<<20 {
			return "", fmt.Errorf("docker log frame too large")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		out.Write(buf)
	}
}
