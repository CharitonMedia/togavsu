package supervisor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const dockerAPI = "/v1.43"

// DockerRuntime talks to the engine over the socket. The core container does
// not get this client.
type DockerRuntime struct {
	Client  *http.Client
	BaseURL string
	Network string
	Env     []string
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
		out = append(out, ContainerInfo{ID: item.ID, Name: name, Image: item.Image, Labels: item.Labels})
	}
	return out, nil
}

func (d *DockerRuntime) StartCore(ctx context.Context, image, name string) (ContainerInfo, error) {
	body := map[string]any{
		"Image":  image,
		"Labels": map[string]string{"togavsu.role": "core", "togavsu.core.active": "0"},
		"Env":    d.Env,
		"HostConfig": map[string]any{
			"NetworkMode": d.Network,
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
	env := append([]string{}, m.Docker.Env...)
	if m.DatabaseURL != "" {
		env = append(env, "DATABASE_URL="+m.DatabaseURL)
	}
	body := map[string]any{
		"Image": image,
		"Cmd":   []string{"migrate"},
		"Env":   env,
		"Tty":   true,
		"HostConfig": map[string]any{
			"NetworkMode": m.Docker.Network,
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
