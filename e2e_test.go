package e2etest

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"
)

var binary = flag.String("binary", "", "The knox binary")

const logSizeLimit = 1024 * 1024

func getStream(f *os.File) (string, error) {
	f.Seek(0, 0)
	fBytes := make([]byte, logSizeLimit)
	n, err := f.Read(fBytes)
	if !errors.Is(err, io.EOF) {
		if err != nil {
			return "", err
		}
		return string(fBytes[:n]), nil
	}
	return "", nil
}

func dumpStream(f *os.File, name string) error {
	fmt.Printf("==== %s ====\n", name)
	stream, err := getStream(f)
	if err != nil {
		return fmt.Errorf("Failed to read %s: %v", name, err)
	}
	fmt.Printf("%s", stream)
	return nil
}

type KnoxProcess struct {
	proc      *os.Process
	stdout    *os.File
	stderr    *os.File
	port      string
	processId string
}

func (kp KnoxProcess) awaitPort() (string, error) {
	const cooldown = 100 * time.Millisecond
	regex, err := regexp.Compile("Listening on .+:([0-9]+)\n")
	if err != nil {
		return "", fmt.Errorf("Failed to compile regex: %v", err)
	}
	for {
		logs, err := getStream(kp.stderr)
		if err != nil {
			return "", err
		}
		if !regex.MatchString(logs) {
			time.Sleep(cooldown)
			continue
		}
		return regex.FindStringSubmatch(logs)[1], nil
	}
	return "", fmt.Errorf("Unreachable code")
}

func NewKnoxProcess(path, datastoreRoot, address, processId string) (KnoxProcess, error) {
	kp := KnoxProcess{
		processId: processId,
	}
	var err error
	kp.stdout, err = os.Create(fmt.Sprintf("knox-stdout-%s", processId))
	if err != nil {
		return KnoxProcess{}, fmt.Errorf("Failed to create stdout: %v", err)
	}

	kp.stderr, err = os.Create(fmt.Sprintf("knox-stderr-%s", processId))
	if err != nil {
		return KnoxProcess{}, fmt.Errorf("Failed to create stderr: %v", err)
	}

	kp.proc, err = os.StartProcess(
		path,
		[]string{
			path,
			"--file-store-root",
			datastoreRoot,
			"--listen-address",
			address,
			"--advertise-address",
			address,
		},
		&os.ProcAttr{
			Files: []*os.File{
				nil,
				kp.stdout,
				kp.stderr,
			},
		},
	)

	if err != nil {
		return KnoxProcess{}, fmt.Errorf("Failed to spawn subprocess: %v", err)
	}

	kp.port, err = kp.awaitPort()
	if err != nil {
		return KnoxProcess{}, fmt.Errorf("Failed to get address from process: %v", err)
	}
	fmt.Printf("Process %s started\n", processId)
	return kp, nil
}

func (kp KnoxProcess) Stop() error {
	if err := kp.proc.Signal(syscall.SIGINT); err != nil {
		return err
	}
	if _, err := kp.proc.Wait(); err != nil {
		return err
	}
	return nil
}

func (kp KnoxProcess) DumpStreams() error {
	if err := dumpStream(kp.stdout, fmt.Sprintf("%s stdout", kp.processId)); err != nil {
		return err
	}
	if err := dumpStream(kp.stderr, fmt.Sprintf("%s stderr", kp.processId)); err != nil {
		return err
	}
	return nil
}

func (kp KnoxProcess) Port() string {
	return kp.port
}

func TestCache(t *testing.T) {
	path, err := filepath.Abs(*binary)
	if err != nil {
		t.Fatalf("Failed to locate binary: %v", err)
	}

	datastoreRoot, err := ioutil.TempDir("", "knox-datastore-test")
	fmt.Printf("Using datastore root %s\n", datastoreRoot)
	if err != nil {
		t.Fatalf("Failed to create test temp dir: %v", err)
	}
	if _, err := os.Stat(*binary); os.IsNotExist(err) {
		t.Fatalf("Missing binary %v", *binary)
	}

	kp, err := NewKnoxProcess(path, datastoreRoot, "localhost:0", "1")
	if err != nil {
		t.Fatalf("Failed to start process: %v\n", err)
	}
	time.Sleep(2 * time.Second)
	kp.Stop()
	kp.DumpStreams()
}
