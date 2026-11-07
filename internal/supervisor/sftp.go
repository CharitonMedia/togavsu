package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
)

// SFTPDestination copies archives with the OpenSSH sftp client.
//
// ponytail: shelling out to sftp instead of an in-process client, ceiling:
// requires the sftp binary, a private key, and a known_hosts file,
// upgrade: an in-process SFTP client when the module dependency set allows
// it, trigger: contracts permit golang.org/x/crypto or CI needs an
// in-process server.
type SFTPDestination struct {
	Spec    DestinationSpec
	Command string
}

func (s *SFTPDestination) ID() string   { return s.Spec.ID }
func (s *SFTPDestination) Kind() string { return "sftp" }

func (s *SFTPDestination) Put(ctx context.Context, name string, r io.Reader, sha string) error {
	if err := safeName(name); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "togavsu-sftp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if sha != "" {
		got, err := hashFile(tmp.Name())
		if err != nil {
			return err
		}
		if got != sha {
			return ErrChecksum
		}
	}
	remote := s.remote(name)
	return s.batch(ctx, "put "+tmp.Name()+" "+remote)
}

func (s *SFTPDestination) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := safeName(name); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "togavsu-sftp-get-*")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	os.Remove(tmp.Name())
	if err := s.batch(ctx, "get "+s.remote(name)+" "+tmp.Name()); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	f, err := os.Open(tmp.Name())
	if err != nil {
		return nil, err
	}
	return &unlinkFile{File: f}, nil
}

func (s *SFTPDestination) Delete(ctx context.Context, name string) error {
	if err := safeName(name); err != nil {
		return err
	}
	return s.batch(ctx, "rm "+s.remote(name))
}

func (s *SFTPDestination) List(ctx context.Context) ([]RemoteObject, error) {
	out, err := s.output(ctx, "ls -1 "+s.Spec.RemotePath)
	if err != nil {
		return nil, err
	}
	var objs []RemoteObject
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "sftp>") {
			continue
		}
		name := path.Base(line)
		if err := safeName(name); err != nil {
			continue
		}
		objs = append(objs, RemoteObject{Name: name})
	}
	return objs, nil
}

func (s *SFTPDestination) Test(ctx context.Context) error {
	return roundTripTest(ctx, s)
}

func (s *SFTPDestination) remote(name string) string {
	return strings.TrimRight(s.Spec.RemotePath, "/") + "/" + name
}

func (s *SFTPDestination) batch(ctx context.Context, commands string) error {
	_, err := s.output(ctx, commands)
	return err
}

func (s *SFTPDestination) output(ctx context.Context, commands string) (string, error) {
	if s.Command == "" {
		s.Command = "sftp"
	}
	batch, err := os.CreateTemp("", "togavsu-sftp-batch-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(batch.Name())
	if _, err := io.WriteString(batch, commands+"\nbye\n"); err != nil {
		batch.Close()
		return "", err
	}
	if err := batch.Close(); err != nil {
		return "", err
	}
	port := s.Spec.Port
	if port == 0 {
		port = 22
	}
	args := []string{
		"-b", batch.Name(),
		"-i", s.Spec.IdentityFile,
		"-P", fmt.Sprintf("%d", port),
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + s.Spec.KnownHosts,
		s.Spec.User + "@" + s.Spec.Host,
	}
	cmd := exec.CommandContext(ctx, s.Command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("sftp: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

type unlinkFile struct{ *os.File }

func (u *unlinkFile) Close() error {
	err := u.File.Close()
	_ = os.Remove(u.Name())
	return err
}
