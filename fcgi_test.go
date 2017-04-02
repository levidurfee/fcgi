package fcgi

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	std "net/http/fcgi"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
)

type mockServer struct {
	list net.Listener
	dir  string
	t    *testing.T
}

func (s *mockServer) Close() error {
	defer os.RemoveAll(s.dir)
	return s.list.Close()
}

func (s *mockServer) Serve(h http.Handler) {
	go func() {
		std.Serve(s.list, h)
	}()
}

func (s *mockServer) Network() string {
	return "unix"
}

func (s *mockServer) Addr() string {
	return filepath.Join(s.dir, "sock")
}

func newServer(t *testing.T) *mockServer {
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}

	sock := filepath.Join(tmp, "sock")

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	return &mockServer{
		list: l,
		dir:  tmp,
		t:    t,
	}
}

func stringSlicesAreSame(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, n := 0, len(a); i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustHaveRequest(
	t *testing.T,
	out io.Reader,
	status int,
	hdrs map[string][]string,
	body []byte) {

	br := bufio.NewReader(out)

	mh, err := textproto.NewReader(br).ReadMIMEHeader()
	if err != nil {
		t.Fatal(err)
	}

	hdr := http.Header(mh)

	s, err := statusFromHeaders(hdr)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range hdrs {
		m := map[string][]string(hdr)
		if !stringSlicesAreSame(v, m[k]) {
			t.Fatalf("Expected header %s to be %v got %v",
				k, v, hdr[k])
		}
	}

	if s != status {
		t.Fatalf("Expected status %d, got %d", status, s)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, br); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(buf.Bytes(), body) {
		t.Fatalf("expected body of:\n%v\ngot:%v\n", body, buf.String())

	}
}

func paramsFor(verb string,
	params map[string][]string) map[string][]string {
	p := map[string][]string{
		"REQUEST_METHOD":  {verb},
		"SERVER_PROTOCOL": {"HTTP/1.1"},
	}

	for key, vals := range params {
		p[key] = vals
	}

	return p
}

func newRandomData(t *testing.T, n int) []byte {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func concat(bs ...[]byte) []byte {
	n := 0
	for _, b := range bs {
		n += len(b)
	}
	buf := make([]byte, n)

	o := 0
	for _, b := range bs {
		copy(buf[o:], b)
		o += len(b)
	}

	return buf
}

func TestConcat(t *testing.T) {
	a := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	b := concat([]byte{0x00}, []byte{0x01, 0x02}, []byte{0x03, 0x04, 0x05})
	if !bytes.Equal(a, b) {
		t.Fatalf("%v vs %v", a, b)
	}
}

func TestStatusOK(t *testing.T) {
	s := newServer(t)
	defer s.Close()

	s.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello FCGI")
	}))

	c, err := Dial(s.Network(), s.Addr())
	if err != nil {
		t.Fatal(err)
	}

	var bout, berr bytes.Buffer
	req, err := c.BeginRequest(
		paramsFor("GET", nil),
		nil, &bout, &berr)
	if err != nil {
		t.Fatal(err)
	}

	if err := req.Wait(); err != nil {
		t.Fatal(err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	mustHaveRequest(t,
		&bout,
		http.StatusOK,
		nil,
		[]byte("Hello FCGI\n"))
}

func TestStatusNotOK(t *testing.T) {
	s := newServer(t)
	defer s.Close()

	s.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "Oh No!")
	}))

	c, err := Dial(s.Network(), s.Addr())
	if err != nil {
		t.Fatal(err)
	}

	var bout, berr bytes.Buffer
	req, err := c.BeginRequest(
		paramsFor("GET", nil),
		nil, &bout, &berr)
	if err != nil {
		t.Fatal(err)
	}

	if err := req.Wait(); err != nil {
		t.Fatal(err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	mustHaveRequest(t,
		&bout,
		http.StatusInternalServerError,
		nil,
		[]byte("Oh No!\n"))
}

func TestWithStdin(t *testing.T) {
	s := newServer(t)
	defer s.Close()

	s.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Fatal(err)
		}
	}))

	c, err := Dial(s.Network(), s.Addr())
	if err != nil {
		t.Fatal(err)
	}

	var bout, berr bytes.Buffer
	req, err := c.BeginRequest(
		paramsFor("GET", nil),
		bytes.NewBufferString("testing\ntesting\ntesting\n"),
		&bout,
		&berr)
	if err != nil {
		t.Fatal(err)
	}

	if err := req.Wait(); err != nil {
		t.Fatal(err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	mustHaveRequest(t,
		&bout,
		http.StatusOK,
		nil,
		[]byte("testing\ntesting\ntesting\n"))

}

func TestWithBigStdin(t *testing.T) {
	s := newServer(t)
	defer s.Close()

	s.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Fatal(err)
		}
	}))

	c, err := Dial(s.Network(), s.Addr())
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, maxWrite+1)

	var bout, berr bytes.Buffer
	req, err := c.BeginRequest(
		paramsFor("GET", nil),
		bytes.NewBuffer(buf),
		&bout,
		&berr)
	if err != nil {
		t.Fatal(err)
	}

	if err := req.Wait(); err != nil {
		t.Fatal(err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	mustHaveRequest(t,
		&bout,
		http.StatusOK,
		nil,
		buf)
}

func TestHeaders(t *testing.T) {
	s := newServer(t)
	defer s.Close()

	s.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO(knorton): There is a bug in the golang fcgi implementation
		// that fails to retain multi-value headers. It only keeps the last.
		if v := r.Header.Get("X-Foo"); v != "B" {
			t.Fatalf("header X-Foo should be [\"B\"], got %v", v)
		}

		if v := r.Header.Get("X-Bar"); v != "False" {
			t.Fatalf("header X-Bar should be [\"False\"], got %v", v)
		}

		w.Header().Add("X-Foo", "A")
		w.Header().Add("X-Foo", "B")
		w.Header().Set("X-Bar", "False")
	}))

	c, err := Dial(s.Network(), s.Addr())
	if err != nil {
		t.Fatal(err)
	}

	var bout, berr bytes.Buffer
	req, err := c.BeginRequest(
		paramsFor("GET", map[string][]string{
			"HTTP_X_FOO": {"A", "B"},
			"HTTP_X_BAR": {"False"},
		}),
		nil,
		&bout,
		&berr)
	if err != nil {
		t.Fatal(err)
	}

	if err := req.Wait(); err != nil {
		t.Fatal(err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	mustHaveRequest(t,
		&bout,
		http.StatusOK,
		map[string][]string{
			"X-Foo": {"A", "B"},
			"X-Bar": {"False"},
		},
		[]byte{})

}

func TestConcurrencyOnStdout(t *testing.T) {
	s := newServer(t)
	defer s.Close()

	m := map[string]chan []byte{
		"A": make(chan []byte),
		"B": make(chan []byte),
	}

	// this provides a sync barrier for each step in this test.
	wall := make(chan struct{})

	s.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.Header.Get("Name")
		for b := range m[name] {
			if _, err := w.Write(b); err != nil {
				t.Fatal(err)
			}
			wall <- struct{}{}
		}
	}))

	c, err := Dial(s.Network(), s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var aout, aerr bytes.Buffer
	ra, err := c.BeginRequest(
		paramsFor("GET", map[string][]string{
			"HTTP_NAME": {"A"},
		}),
		nil, &aout, &aerr)
	if err != nil {
		t.Fatal(err)
	}

	var bout, berr bytes.Buffer
	rb, err := c.BeginRequest(
		paramsFor("GET", map[string][]string{
			"HTTP_NAME": {"B"},
		}),
		nil, &bout, &berr)
	if err != nil {
		t.Fatal(err)
	}

	aData := newRandomData(t, 2048)
	bData := newRandomData(t, 2048)

	go func() {
		if err := ra.Wait(); err != nil {
			t.Fatal(err)
		}

		mustHaveRequest(t,
			&aout,
			http.StatusOK,
			nil,
			aData)

		wall <- struct{}{}
	}()

	go func() {
		if err := rb.Wait(); err != nil {
			t.Fatal(err)
		}

		mustHaveRequest(t,
			&bout,
			http.StatusOK,
			nil,
			bData)

		wall <- struct{}{}
	}()

	// This ensures that the two requests send their stdout back in an
	// interleaved fashion.
	m["B"] <- bData[:1024]
	<-wall

	m["A"] <- aData[:1024]
	<-wall

	m["B"] <- bData[1024:]
	<-wall

	m["A"] <- aData[1024:]
	<-wall

	close(m["B"])
	close(m["A"])

	<-wall
	<-wall
}
