package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	expv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultUserName       = "root"
	SSHPrivateKeyFilePath = "VD_SSH_PRIVATE_KEY_PATH"
)

// MachineLogCollector implements log collection for VMware Desktop machines.
type MachineLogCollector struct {
}

// CollectInfrastructureLogs implements framework.ClusterLogCollector.
func (m MachineLogCollector) CollectInfrastructureLogs(
	ctx context.Context,
	managementClusterClient client.Client,
	c *v1beta1.Cluster,
	outputPath string,
) error {
	return nil
}

// CollectMachinePoolLog implements framework.ClusterLogCollector.
func (MachineLogCollector) CollectMachinePoolLog(
	ctx context.Context,
	managementClusterClient client.Client,
	m *expv1.MachinePool,
	outputPath string,
) error {
	return nil
}

// CollectMachineLog implements framework.ClusterLogCollector.
func (MachineLogCollector) CollectMachineLog(
	ctx context.Context,
	managementClusterClient client.Client,
	m *v1beta1.Machine,
	outputPath string,
) error {
	if len(m.Status.Addresses) == 0 {
		return errors.Errorf("machine %s has no addresses", klog.KObj(m))
	}
	address := m.Status.Addresses[0].Address

	captureLogs := func(hostFileName, command string, args ...string) func() error {
		return func() error {
			f, err := createOutputFile(filepath.Join(outputPath, hostFileName))
			if err != nil {
				return err
			}
			defer func() {
				if cerr := f.Close(); cerr != nil {
					err = errors.Wrap(cerr, "failed to close output file")
				}
			}()
			if err := executeRemoteCommand(f, address, command, args...); err != nil {
				return errors.Wrapf(err, "failed to run command %s for machine %s on ips [%s]", command, klog.KObj(m), address)
			}
			return nil
		}
	}

	return aggregateConcurrent(
		captureLogs("kubelet.log",
			"journalctl", "--no-pager", "--output=short-precise", "-u", "kubelet.service"),
		captureLogs("containerd.log",
			"journalctl", "--no-pager", "--output=short-precise", "-u", "containerd.service"),
		captureLogs("cloud-init.log",
			"cat", "/var/log/cloud-init.log"),
		captureLogs("cloud-init-output.log",
			"cat", "/var/log/cloud-init-output.log"),
	)
}

func createOutputFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	return os.Create(filepath.Clean(path))
}

func executeRemoteCommand(f io.StringWriter, hostIPAddr, command string, args ...string) error {
	config, err := newSSHConfig()
	if err != nil {
		return err
	}
	port := "22"

	hostClient, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", hostIPAddr, port), config)
	if err != nil {
		return errors.Wrapf(err, "dialing host IP address at %s", hostIPAddr)
	}
	defer func() {
		if cerr := hostClient.Close(); cerr != nil {
			err = errors.Wrap(cerr, "failed to close SSH client")
		}
	}()

	session, err := hostClient.NewSession()
	if err != nil {
		return errors.Wrap(err, "opening SSH session")
	}
	defer func() {
		if cerr := session.Close(); cerr != nil {
			err = errors.Wrap(cerr, "failed to close SSH session")
		}
	}()

	// Run the command and write the captured stdout to the file
	var stdoutBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	if err = session.Run(command); err != nil {
		return errors.Wrapf(err, "running command \"%s\"", command)
	}
	if _, err = f.WriteString(stdoutBuf.String()); err != nil {
		return errors.Wrap(err, "writing output to file")
	}

	return nil
}

// newSSHConfig returns a configuration to use for SSH connections to remote machines.
func newSSHConfig() (*ssh.ClientConfig, error) {
	sshPrivateKeyContent, err := readPrivateKey()
	if err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(sshPrivateKeyContent)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing private key: %s", sshPrivateKeyContent)
	}

	config := &ssh.ClientConfig{
		User:            DefaultUserName,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // Non-production code
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
	}

	return config, nil
}

func readPrivateKey() ([]byte, error) {
	privateKeyFilePath := os.Getenv(SSHPrivateKeyFilePath)
	if privateKeyFilePath == "" {
		return nil, errors.Errorf("private key missing. Please set %s env var", SSHPrivateKeyFilePath)
	}

	return os.ReadFile(filepath.Clean(privateKeyFilePath))
}

// aggregateConcurrent runs fns concurrently, returning aggregated errors.
func aggregateConcurrent(funcs ...func() error) error {
	// run all fns concurrently
	ch := make(chan error, len(funcs))
	var wg sync.WaitGroup
	for _, fn := range funcs {
		wg.Add(1)
		go func(f func() error) {
			defer wg.Done()
			ch <- f()
		}(fn)
	}
	wg.Wait()
	close(ch)
	// collect up and return errors
	errs := []error{}
	for err := range ch {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return kerrors.NewAggregate(errs)
}
