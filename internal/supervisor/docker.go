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

// dockerWaitLimit bounds an engine call whose caller did not set a deadline.
// Scheduled updates use the process lifetime context, so a stalled inspect,
// wait, or pull would otherwise hold the operation gate until shutdown.
var dockerWaitLimit = 2 * time.Minute

// boundDocker keeps a caller deadline that is already set. A context with no
// deadline gets one, so the HTTP request itself stops waiting.
func boundDocker(ctx context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	if limit <= 0 {
		limit = dockerWaitLimit
	}
	return context.WithTimeout(ctx, limit)
}

var errDockerNotModified = errors.New("container already running")

// DockerRuntime talks to the engine over the socket. The core container does
// not get this client.
type DockerRuntime struct {
	Client           *http.Client
	BaseURL          string
	Network          string
	AppSecretsVolume string
	// CoreDataVolume is the core's own volume (upload staging and the default
	// local-disk file store); a replacement core must mount the same one.
	CoreDataVolume string
	Env            []string
	Binds          []string
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

// CoreDataDir is where the core image keeps its data volume (core.DataDir).
const CoreDataDir = "/var/lib/togavsu-core"

// CoreBinds mounts the installer secret volume and the core's data volume, as the
// Compose core service does. The default names match the Compose project name togavsu.
// Without the data volume a replacement core would start on an empty local-disk file
// store.
func CoreBinds(secrets, data string) []string {
	if secrets == "" {
		secrets = "togavsu_appsecrets"
	}
	if data == "" {
		data = "togavsu_core_data"
	}
	return []string{secrets + ":/appsecrets", data + ":" + CoreDataDir}
}

func (d *DockerRuntime) coreEnv() []string {
	if len(d.Env) == 0 {
		return CoreEnv()
	}
	return d.Env
}

func (d *DockerRuntime) coreBinds() []string {
	if len(d.Binds) == 0 {
		return CoreBinds(d.AppSecretsVolume, d.CoreDataVolume)
	}
	return d.Binds
}

func DockerSocketClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: dockerWaitLimit,
		Transport: &http.Transport{
			ResponseHeaderTimeout: dockerWaitLimit,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

func dockerHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{
		Timeout:   dockerWaitLimit,
		Transport: &http.Transport{ResponseHeaderTimeout: dockerWaitLimit},
	}
}

func (d *DockerRuntime) base() string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	return "http://docker"
}

func (d *DockerRuntime) ListCores(ctx context.Context) ([]ContainerInfo, error) {
	cores, err := d.list(ctx, map[string][]string{"label": {"togavsu.role=core"}}, true)
	if err != nil {
		return nil, err
	}
	return d.withAliases(ctx, cores)
}

func (d *DockerRuntime) ListAllCores(ctx context.Context) ([]ContainerInfo, error) {
	cores, err := d.list(ctx, map[string][]string{"label": {"togavsu.role=core"}}, false)
	if err != nil {
		return nil, err
	}
	return d.withAliases(ctx, cores)
}

// withAliases fills network aliases from inspect. Docker's container list leaves
// them null; without them the Compose core's "core" alias, which Caddy dials,
// matches no container and every update is refused as proxy_unknown. A core
// gone since the list is dropped; any other inspect error fails the list.
func (d *DockerRuntime) withAliases(ctx context.Context, cores []ContainerInfo) ([]ContainerInfo, error) {
	out := cores[:0]
	for _, c := range cores {
		if len(c.Aliases) == 0 {
			found, err := d.Inspect(ctx, c.ID)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			c.Aliases = found.Aliases
		}
		out = append(out, c)
	}
	return out, nil
}

func (d *DockerRuntime) list(ctx context.Context, filters map[string][]string, runningOnly bool) ([]ContainerInfo, error) {
	network := strings.TrimSpace(d.Network)
	if network == "" {
		return nil, errors.New("docker network is not configured")
	}
	if filters == nil {
		filters = map[string][]string{}
	}
	filters["network"] = []string{network}
	raw, err := json.Marshal(filters)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("all", "1")
	q.Set("filters", string(raw))
	var listed []struct {
		ID              string            `json:"Id"`
		Names           []string          `json:"Names"`
		Image           string            `json:"Image"`
		Labels          map[string]string `json:"Labels"`
		State           string            `json:"State"`
		NetworkSettings struct {
			Networks map[string]struct {
				Aliases  []string `json:"Aliases"`
				DNSNames []string `json:"DNSNames"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := d.call(ctx, http.MethodGet, dockerAPI+"/containers/json?"+q.Encode(), nil, &listed); err != nil {
		return nil, err
	}
	var out []ContainerInfo
	for _, item := range listed {
		if runningOnly && item.State != "" && item.State != "running" {
			continue
		}
		if _, ours := item.NetworkSettings.Networks[network]; !ours {
			continue
		}
		name := ""
		if len(item.Names) > 0 {
			name = strings.TrimPrefix(item.Names[0], "/")
		}
		state := item.State
		running := state == "running"
		ours := map[string]struct {
			Aliases  []string `json:"Aliases"`
			DNSNames []string `json:"DNSNames"`
		}{network: item.NetworkSettings.Networks[network]}
		out = append(out, ContainerInfo{ID: item.ID, Name: name, Image: item.Image, Labels: item.Labels, Aliases: networkAliases(item.Names, ours), State: state, Running: running})
	}
	return out, nil
}

func (d *DockerRuntime) Pull(ctx context.Context, image string) error {
	if err := ImmutableImage(image); err != nil {
		return err
	}
	q := url.Values{}
	q.Set("fromImage", image)
	ctx, cancel := boundDocker(ctx, dockerWaitLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base()+dockerAPI+"/images/create?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	res, err := dockerHTTPClient(d.Client).Do(req)
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
		NetworkSettings struct {
			Networks map[string]struct {
				Aliases  []string `json:"Aliases"`
				DNSNames []string `json:"DNSNames"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := d.call(ctx, http.MethodGet, dockerAPI+"/containers/"+id+"/json", nil, &inspected); err != nil {
		return ContainerInfo{}, err
	}
	state := inspected.State.Status
	running := inspected.State.Running || state == "running"
	gotID := inspected.ID
	if gotID == "" {
		gotID = id
	}
	if network := strings.TrimSpace(d.Network); network != "" {
		if _, ours := inspected.NetworkSettings.Networks[network]; !ours {
			return ContainerInfo{}, ErrNotFound
		}
	}
	name := strings.TrimPrefix(inspected.Name, "/")
	aliases := inspected.NetworkSettings.Networks
	if network := strings.TrimSpace(d.Network); network != "" {
		aliases = map[string]struct {
			Aliases  []string `json:"Aliases"`
			DNSNames []string `json:"DNSNames"`
		}{network: inspected.NetworkSettings.Networks[network]}
	}
	return ContainerInfo{ID: gotID, Name: name, Aliases: networkAliases([]string{"/" + name}, aliases), State: state, Running: running}, nil
}

func networkAliases(names []string, networks map[string]struct {
	Aliases  []string `json:"Aliases"`
	DNSNames []string `json:"DNSNames"`
}) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(alias string) {
		alias = strings.TrimPrefix(strings.TrimSpace(alias), "/")
		if alias == "" {
			return
		}
		if _, ok := seen[alias]; ok {
			return
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	for i, name := range names {
		if i == 0 {
			continue
		}
		add(name)
	}
	for _, net := range networks {
		for _, alias := range net.Aliases {
			add(alias)
		}
		for _, alias := range net.DNSNames {
			add(alias)
		}
	}
	return out
}

func (d *DockerRuntime) CreateCore(ctx context.Context, image, name string) (ContainerInfo, error) {
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
		info, ierr := d.Inspect(ctx, name)
		if ierr != nil || info.ID == "" {
			return ContainerInfo{}, err
		}
		return info, nil
	}
	return ContainerInfo{ID: created.ID, Name: name, Image: image}, nil
}

func (d *DockerRuntime) StartCore(ctx context.Context, image, name string) (ContainerInfo, error) {
	info, err := d.CreateCore(ctx, image, name)
	if err != nil {
		return ContainerInfo{}, err
	}
	if err := d.Start(ctx, info.ID); err != nil {
		found, ierr := d.Inspect(ctx, info.ID)
		if ierr != nil || !found.Running {
			return info, err
		}
	}
	return info, nil
}

// ConfirmAbsent stops and removes a container on a fresh bounded context, then
// inspects it. A missing container is success. A failed inspect stays pending.
func (d *DockerRuntime) ConfirmAbsent(ctx context.Context, id string) error {
	clean, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = ctx
	stopErr := d.Stop(clean, id)
	remErr := d.Remove(clean, id)
	_, err := d.Inspect(clean, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("container %s still present: %w", id, errors.Join(stopErr, remErr))
}

func (d *DockerRuntime) WaitHealthy(ctx context.Context, id string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = time.Minute
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%w: timed out after %s", ErrHealth, timeout)
		}
		reqCtx, cancel := context.WithTimeout(ctx, remaining)
		var inspected struct {
			State struct {
				Status string `json:"Status"`
				Health *struct {
					Status string `json:"Status"`
				} `json:"Health"`
			} `json:"State"`
		}
		err := d.call(reqCtx, http.MethodGet, dockerAPI+"/containers/"+id+"/json", nil, &inspected)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("%w: timed out after %s", ErrHealth, timeout)
			}
			return err
		}
		health := ""
		if inspected.State.Health != nil {
			health = inspected.State.Health.Status
		}
		if health == "" || health == "none" {
			return fmt.Errorf("%w: core image has no health check", ErrHealth)
		}
		status := strings.ToLower(strings.TrimSpace(inspected.State.Status))
		switch health {
		case "healthy":
			if status == "running" {
				return nil
			}
			return fmt.Errorf("%w: container is %s", ErrHealth, status)
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
	err = d.call(ctx, http.MethodPost, dockerAPI+"/containers/"+found[0].ID+"/"+action, nil, nil)
	if errors.Is(err, errDockerNotModified) {
		// Already started or already stopped.
		return nil
	}
	return err
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
	ctx, cancel := boundDocker(ctx, dockerWaitLimit)
	defer cancel()
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
	res, err := dockerHTTPClient(d.Client).Do(req)
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
// The image must print migrated=true when it applied a migration. The
// container gets the database URL the same way the running core does: the
// image entrypoint reads /appsecrets/database_url and exports
// TOGAVSU_DATABASE_URL. The supervisor does not put the URL in the container
// environment.
type DockerMigrator struct {
	Docker *DockerRuntime
}

func (m DockerMigrator) Apply(ctx context.Context, image string) (bool, error) {
	name, err := nextMigrateName(time.Now())
	if err != nil {
		return false, err
	}
	st, err := m.RunMigration(ctx, image, name, func(string, string) error { return nil })
	if !st.Known {
		if err == nil {
			err = ErrMigrationUnknown
		}
		return false, err
	}
	return st.Ran, err
}

func (m DockerMigrator) RunMigration(ctx context.Context, image, name string, before func(phase, id string) error) (migrationStatus, error) {
	if m.Docker == nil || m.Docker.Client == nil {
		return migrationStatus{Name: name}, ErrMigratorUnconfigured
	}
	if before == nil {
		before = func(string, string) error { return nil }
	}
	if err := before("migration_intended", ""); err != nil {
		return migrationStatus{Name: name}, err
	}
	if err := m.Docker.Pull(ctx, image); err != nil {
		return migrationStatus{Name: name}, err
	}
	id, err := m.createOrFind(ctx, image, name)
	if err != nil {
		pending, cerr := m.finishMigrationContainer(name)
		if pending || cerr != nil {
			return migrationStatus{Name: name, CleanupPending: true}, errors.Join(err, cerr)
		}
		return migrationStatus{Name: name}, errors.Join(err, cerr)
	}
	if err := before("migration_created", id); err != nil {
		pending, cerr := m.finishMigrationContainer(id)
		return migrationStatus{Name: name, ID: id, CleanupPending: pending}, errors.Join(err, cerr)
	}
	if err := before("migration_starting", id); err != nil {
		pending, cerr := m.finishMigrationContainer(id)
		return migrationStatus{Name: name, ID: id, CleanupPending: pending}, errors.Join(err, cerr)
	}
	if err := m.Docker.Start(ctx, id); err != nil {
		info, ierr := m.Docker.Inspect(ctx, id)
		if ierr != nil || !info.Running {
			pending, cerr := m.finishMigrationContainer(id)
			return migrationStatus{Name: name, ID: id, CleanupPending: pending}, errors.Join(err, cerr)
		}
	}
	var waited struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := m.Docker.call(ctx, http.MethodPost, dockerAPI+"/containers/"+id+"/wait", nil, &waited); err != nil {
		pending, cerr := m.finishMigrationContainer(id)
		return migrationStatus{Name: name, ID: id, CleanupPending: pending}, errors.Join(fmt.Errorf("%w: %v", ErrMigrationUnknown, err), cerr)
	}
	logs, err := m.logs(ctx, id)
	pending, cerr := m.finishMigrationContainer(id)
	st := migrationStatus{Name: name, ID: id, CleanupPending: pending}
	if err != nil {
		return st, errors.Join(fmt.Errorf("%w: %v", ErrMigrationUnknown, err), cerr)
	}
	st.Known = true
	st.Ran = strings.Contains(logs, "migrated=true")
	if cerr != nil {
		return st, cerr
	}
	if waited.StatusCode != 0 {
		return st, fmt.Errorf("migrate exited %d", waited.StatusCode)
	}
	return st, nil
}

func (m DockerMigrator) createOrFind(ctx context.Context, image, name string) (string, error) {
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
	err := m.Docker.call(ctx, http.MethodPost, dockerAPI+"/containers/create?name="+url.QueryEscape(name), body, &created)
	if err == nil && created.ID != "" {
		return created.ID, nil
	}
	info, ierr := m.Docker.Inspect(ctx, name)
	if ierr == nil && info.ID != "" {
		return info.ID, nil
	}
	if err != nil {
		return "", err
	}
	return "", ierr
}

func (m DockerMigrator) finishMigrationContainer(id string) (bool, error) {
	if err := m.Docker.ConfirmAbsent(context.Background(), id); err != nil {
		return true, err
	}
	return false, nil
}

func (m DockerMigrator) logs(ctx context.Context, id string) (string, error) {
	ctx, cancel := boundDocker(ctx, dockerWaitLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Docker.base()+dockerAPI+"/containers/"+id+"/logs?stdout=1&stderr=1", nil)
	if err != nil {
		return "", err
	}
	res, err := dockerHTTPClient(m.Docker.Client).Do(req)
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
