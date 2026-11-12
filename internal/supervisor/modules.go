package supervisor

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ModuleDeployment is the core's PUT /v1/modules/{name} body (internal/modules.Deployment).
// Secrets become files under ModuleSecretsDir in the module container; they are never
// placed in the container environment, labels or the supervisor's disk.
type ModuleDeployment struct {
	Module  string            `json:"module"`
	Version string            `json:"version"`
	Image   string            `json:"image"`
	Digest  string            `json:"digest"`
	Port    int               `json:"port"`
	Secrets map[string]string `json:"secrets"`
}

// ModuleRuntime creates and removes module containers. DockerRuntime implements it.
type ModuleRuntime interface {
	DeployModule(ctx context.Context, d ModuleDeployment) error
	RemoveModule(ctx context.Context, name string) error
}

// ModuleSecretsDir is where a module reads its secret files (the Docker secrets
// convention the sample module's -secrets flag defaults to).
const ModuleSecretsDir = "/run/secrets"

// ModuleUser is the unprivileged uid:gid every module container runs as.
const ModuleUser = "65532:65532"

const (
	moduleUID          = 65532
	maxModuleSecrets   = 32
	maxModuleSecretLen = 64 << 10
)

// ModuleContainerName is the container name and network alias of a module; the core
// reaches it at http://togavsu-mod-{id}:{port} (internal/core.ModuleURL).
func ModuleContainerName(name string) string { return "togavsu-mod-" + name }

// ModuleImageRef pins the manifest image to its digest.
func ModuleImageRef(image, digest string) string { return image + "@" + digest }

// Validate checks the deployment against the path name before Docker sees any of it.
func (d ModuleDeployment) Validate(name string) error {
	if err := safeModule(name); err != nil {
		return err
	}
	if d.Module != name {
		return fmt.Errorf("%w: module %q does not match the path", ErrBadRequest, d.Module)
	}
	if d.Image == "" || strings.Contains(d.Image, "@") {
		return fmt.Errorf("%w: module image must be a name without a digest", ErrBadRequest)
	}
	if err := ImmutableImage(ModuleImageRef(d.Image, d.Digest)); err != nil {
		return fmt.Errorf("%w: module image must be pinned to sha256:<64 hex chars>", ErrBadRequest)
	}
	if d.Port < 1 || d.Port > 65535 {
		return fmt.Errorf("%w: module port", ErrBadRequest)
	}
	if len(d.Secrets) > maxModuleSecrets {
		return fmt.Errorf("%w: too many module secrets", ErrBadRequest)
	}
	for key, val := range d.Secrets {
		if !safeSecretName(key) {
			return fmt.Errorf("%w: module secret name %q", ErrBadRequest, key)
		}
		if len(val) > maxModuleSecretLen {
			return fmt.Errorf("%w: module secret %s is too large", ErrBadRequest, key)
		}
	}
	return nil
}

func safeSecretName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// moduleSecretsTar is the archive uploaded into the created, not yet started,
// container: one 0400 file per secret owned by the module user.
func moduleSecretsTar(secrets map[string]string, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dir := strings.TrimPrefix(ModuleSecretsDir, "/") + "/"
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir, Mode: 0o500, Uid: moduleUID, Gid: moduleUID, ModTime: now}); err != nil {
		return nil, err
	}
	for key, val := range secrets {
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: dir + key, Mode: 0o400, Uid: moduleUID, Gid: moduleUID, Size: int64(len(val)), ModTime: now}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(val)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DeployModule replaces the module's container with one created from the pinned image,
// with its secrets written as files and the alias togavsu-mod-{id} on the stack network.
// The container is left created; POST /v1/modules/{name}/start runs it.
//
// ponytail: secrets live in the container's writable layer (removed with the container),
// ceiling: they sit on the Docker host disk while the container exists, upgrade: a tmpfs
// mount populated by an init step, trigger: phase 7 release hardening or a host-disk audit.
func (d *DockerRuntime) DeployModule(ctx context.Context, dep ModuleDeployment) error {
	if err := dep.Validate(dep.Module); err != nil {
		return err
	}
	if strings.TrimSpace(d.Network) == "" {
		return errors.New("docker network is not configured")
	}
	ref := ModuleImageRef(dep.Image, dep.Digest)
	if err := d.ensureImage(ctx, ref); err != nil {
		return err
	}
	if err := d.RemoveModule(ctx, dep.Module); err != nil {
		return err
	}
	name := ModuleContainerName(dep.Module)
	port := strconv.Itoa(dep.Port) + "/tcp"
	body := map[string]any{
		"Image": ref,
		"User":  ModuleUser,
		"Labels": map[string]string{
			"togavsu.role":           "module",
			"togavsu.module":         dep.Module,
			"togavsu.module.version": dep.Version,
		},
		"ExposedPorts": map[string]any{port: map[string]any{}},
		"HostConfig": map[string]any{
			"NetworkMode":   d.Network,
			"CapDrop":       []string{"ALL"},
			"SecurityOpt":   []string{"no-new-privileges:true"},
			"RestartPolicy": map[string]any{"Name": "unless-stopped"},
		},
		"NetworkingConfig": map[string]any{
			"EndpointsConfig": map[string]any{
				d.Network: map[string]any{"Aliases": []string{name}},
			},
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := d.call(ctx, http.MethodPost, dockerAPI+"/containers/create?name="+url.QueryEscape(name), body, &created); err != nil {
		return fmt.Errorf("create module container: %w", err)
	}
	archive, err := moduleSecretsTar(dep.Secrets, time.Now())
	if err != nil {
		return errors.Join(err, d.Remove(ctx, created.ID))
	}
	if err := d.putArchive(ctx, created.ID, "/", archive); err != nil {
		return errors.Join(fmt.Errorf("write module secrets: %w", err), d.Remove(ctx, created.ID))
	}
	return nil
}

// RemoveModule force-removes every container labelled for the module. A module with
// no container is already removed.
func (d *DockerRuntime) RemoveModule(ctx context.Context, name string) error {
	if err := safeModule(name); err != nil {
		return err
	}
	found, err := d.list(ctx, map[string][]string{"label": {"togavsu.module=" + name}}, false)
	if err != nil {
		return err
	}
	for _, c := range found {
		if err := d.Remove(ctx, c.ID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}

// ensureImage uses a local copy of the pinned image when the engine has one (an
// air-gapped load) and pulls it otherwise.
func (d *DockerRuntime) ensureImage(ctx context.Context, ref string) error {
	err := d.call(ctx, http.MethodGet, dockerAPI+"/images/"+ref+"/json", nil, nil)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	return d.Pull(ctx, ref)
}

func (d *DockerRuntime) putArchive(ctx context.Context, id, path string, archive []byte) error {
	ctx, cancel := boundDocker(ctx, dockerWaitLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, d.base()+dockerAPI+"/containers/"+id+"/archive?path="+url.QueryEscape(path), bytes.NewReader(archive))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	res, err := dockerHTTPClient(d.Client).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("docker archive %s", res.Status)
	}
	return nil
}
