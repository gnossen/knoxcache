package e2etest

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	enc "github.com/gnossen/knoxcache/encoder"
)

var binary = flag.String("binary", "", "The knox binary")

const logSizeLimit = 1024 * 1024

func getKnoxBinary(t *testing.T) string {
	path, err := filepath.Abs(*binary)
	if err != nil {
		t.Fatalf("Failed to locate binary: %v", err)
	}

	if _, err := os.Stat(*binary); os.IsNotExist(err) {
		t.Fatalf("Missing binary %v", *binary)
	}

	return path
}

func makeDatastoreRoot(t *testing.T) string {
	datastoreRoot, err := ioutil.TempDir("", "knox-datastore-test")
	if err != nil {
		t.Fatalf("Failed to create test temp dir: %v", err)
	}
	return datastoreRoot
}

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
	banner := fmt.Sprintf("==== %s ====", name)
	fmt.Printf("%s\n", banner)
	stream, err := getStream(f)
	if err != nil {
		return fmt.Errorf("Failed to read %s: %v", name, err)
	}
	fmt.Printf("%s", stream)
	tailer := strings.Repeat("=", len(banner))
	fmt.Printf("%s\n", tailer)
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

func (kp KnoxProcess) Close() error {
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

func (kp KnoxProcess) Get(rawUrl string) (*http.Response, error) {
	encoder := enc.NewDefaultEncoder()
	requestUrlHash, err := encoder.Encode(rawUrl)
	if err != nil {
		return nil, err
	}
	requestUrl := fmt.Sprintf("http://localhost:%s/c/%s", kp.Port(), requestUrlHash)
	return http.Get(requestUrl)
}

type HttpHandler func(http.ResponseWriter, *http.Request)

type HttpHandlerConfig map[string]HttpHandler

func cannedContent(body string) HttpHandler {
	return func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}
}

type TestHandler struct {
	UriCounts map[string]int
	config    HttpHandlerConfig
	mu        sync.Mutex
}

func (th *TestHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	th.mu.Lock()
	count, ok := th.UriCounts[req.URL.Path]
	if !ok {
		count = 0
	}
	th.UriCounts[req.URL.Path] = count + 1
	th.mu.Unlock()
	for uri, handler := range th.config {
		if strings.HasPrefix(req.URL.Path, uri) {
			handler(w, req)
			return
		}
	}
	w.WriteHeader(404)
	io.WriteString(w, fmt.Sprintf("URI %s is invalid.", req.URL.Path))
}

func NewTestHttpServer(hc HttpHandlerConfig) (srv *http.Server, th *TestHandler, address string, err error) {
	th = &TestHandler{map[string]int{}, hc, sync.Mutex{}}

	listenAddress := "localhost:0"
	srv = &http.Server{Addr: listenAddress, Handler: th}
	ln, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, nil, "", fmt.Errorf("Failed to listen on %s: %v", listenAddress, err)
	}
	go srv.Serve(ln)
	return srv, th, ln.Addr().String(), nil
}

func getHttpResponseBody(res *http.Response, t *testing.T) string {
	buf := new(strings.Builder)
	_, err := io.Copy(buf, res.Body)
	if err != nil {
		t.Fatalf("Failed to copy content from HTTP response: %v", err)
	}

	return buf.String()
}

func TestCache(t *testing.T) {
	path := getKnoxBinary(t)
	datastoreRoot := makeDatastoreRoot(t)

	kp, err := NewKnoxProcess(path, datastoreRoot, "localhost:0", "1")
	if err != nil {
		t.Fatalf("Failed to start process: %v\n", err)
	}
	defer kp.Close()
	defer kp.DumpStreams()

	body := "testing123"
	testServer, th, testServerAddress, err := NewTestHttpServer(
		HttpHandlerConfig{
			"/test1": cannedContent(body),
		},
	)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer testServer.Close()

	encoder := enc.NewDefaultEncoder()
	rawUrl := fmt.Sprintf("http://%s/test1", testServerAddress)
	requestUrlHash, err := encoder.Encode(rawUrl)
	if err != nil {
		t.Fatalf("%v", err)
	}

	requestUrl := fmt.Sprintf("http://localhost:%s/c/%s", kp.Port(), requestUrlHash)
	res, err := http.Get(requestUrl)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	evaluateResponse := func(res *http.Response) {
		if res.StatusCode != 200 {
			t.Fatalf("Expected status code 200 but found %d", res.StatusCode)
		}

		gotBody := getHttpResponseBody(res, t)
		if gotBody != body {
			t.Fatalf("Wrong content. got = \"%s\", want = \"%s\".", gotBody, body)
		}

		expectedCounts := map[string]int{
			"/test1": 1,
		}

		if !reflect.DeepEqual(th.UriCounts, expectedCounts) {
			t.Fatalf("URI request counts are not right. got = %v\n want = %v\n", th.UriCounts, expectedCounts)
		}
	}

	evaluateResponse(res)

	res, err = http.Get(requestUrl)
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}

	evaluateResponse(res)
}

func TestConcurrentCreationSingleKnox(t *testing.T) {
	path := getKnoxBinary(t)
	datastoreRoot := makeDatastoreRoot(t)

	kp, err := NewKnoxProcess(path, datastoreRoot, "localhost:0", "1")
	if err != nil {
		t.Fatalf("Failed to start process: %v\n", err)
	}
	defer kp.Close()
	defer kp.DumpStreams()

	body := "testing123"
	testServer, th, testServerAddress, err := NewTestHttpServer(
		HttpHandlerConfig{
			"/test1": cannedContent(body),
		},
	)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer testServer.Close()

	rawUrl := fmt.Sprintf("http://%s/test1", testServerAddress)

	type Result struct {
		Res *http.Response
		Err error
	}

	const clientCount = 32
	resultChan := make(chan Result)
	var wg sync.WaitGroup
	wg.Add(clientCount)

	runClient := func() {
		wg.Done()
		wg.Wait()
		res, err := kp.Get(rawUrl)
		resultChan <- Result{res, err}
	}

	for i := 0; i < clientCount; i += 1 {
		go runClient()
	}

	var results []Result
	for i := 0; i < clientCount; i += 1 {
		result := <-resultChan
		results = append(results, result)
	}

	for _, res := range results {
		if res.Err != nil {
			t.Errorf("Response not completed successfullly: %v", res.Err)
			continue
		}
		if res.Res == nil {
			t.Errorf("Received nil response.")
			continue
		}
		if res.Res.StatusCode != 200 {
			t.Errorf("Wrong response code. got = %d, want = %d.", res.Res.StatusCode, 200)
		}

		gotBody := getHttpResponseBody(res.Res, t)
		if gotBody != body {
			t.Errorf("Wrong http content. got = \"%s\", want = \"%s\"", gotBody, body)
		}
	}

	expectedCounts := map[string]int{
		"/test1": 1,
	}

	if !reflect.DeepEqual(th.UriCounts, expectedCounts) {
		t.Errorf("URI request counts are not right. got = %v\n want = %v\n", th.UriCounts, expectedCounts)
	}
}

func TestConcurrentCreationMultipleKnox(t *testing.T) {
	path := getKnoxBinary(t)
	datastoreRoot := makeDatastoreRoot(t)

	const knoxInstanceCount = 4
	var knoxes []KnoxProcess

	for i := 0; i < knoxInstanceCount; i += 1 {
		kp, err := NewKnoxProcess(path, datastoreRoot, "localhost:0", strconv.FormatInt(int64(i), 10))
		if err != nil {
			t.Fatalf("Failed to start process: %v\n", err)
		}
		defer kp.Close()
		defer kp.DumpStreams()
		knoxes = append(knoxes, kp)
	}

	body := "testing123"
	testServer, th, testServerAddress, err := NewTestHttpServer(
		HttpHandlerConfig{
			"/test1": cannedContent(body),
		},
	)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer testServer.Close()

	type Result struct {
		Res *http.Response
		Err error
	}

	const perKnoxClientCount = 16

	resultChan := make(chan Result)
	var wg sync.WaitGroup
	wg.Add(perKnoxClientCount * knoxInstanceCount)

	runClient := func(kp KnoxProcess, rawUrl string) {
		wg.Done()
		wg.Wait()
		res, err := kp.Get(rawUrl)
		resultChan <- Result{res, err}
	}

	for i := 0; i < knoxInstanceCount; i += 1 {
		rawUrl := fmt.Sprintf("http://%s/test1", testServerAddress)

		for j := 0; j < perKnoxClientCount; j += 1 {
			go runClient(knoxes[i], rawUrl)
		}
	}

	var results []Result
	for i := 0; i < perKnoxClientCount*knoxInstanceCount; i += 1 {
		result := <-resultChan
		results = append(results, result)
	}

	for _, res := range results {
		if res.Err != nil {
			t.Errorf("Response not completed successfully: %v", res.Err)
			continue
		}
		if res.Res == nil {
			t.Errorf("Received nil response.")
			continue
		}
		if res.Res.StatusCode != 200 {
			t.Errorf("Wrong response code. got = %d, want = %d.", res.Res.StatusCode, 200)
		}

		gotBody := getHttpResponseBody(res.Res, t)
		if gotBody != body {
			t.Errorf("Wrong http content. got = \"%s\", want = \"%s\"", gotBody, body)
		}
	}

	expectedCounts := map[string]int{
		"/test1": 1,
	}

	if !reflect.DeepEqual(th.UriCounts, expectedCounts) {
		t.Errorf("URI request counts are not right. got = %v\n want = %v\n", th.UriCounts, expectedCounts)
	}
}

// TODO: Test a long-lived download.
// TODO: Test a process that dies in the middle of a download.
