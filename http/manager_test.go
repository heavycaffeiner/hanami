package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func testAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func testConfig(address string, handler nethttp.Handler) ServerConfig {
	return ServerConfig{
		Address:                address,
		Protocol:               ProtocolHTTP,
		Handler:                handler,
		ProbePath:              "/probe",
		ProbeIdentity:          "test-generation",
		ProbeTimeout:           time.Second,
		DrainTimeout:           time.Second,
		MaxDrainingGenerations: 2,
	}
}

func TestManagerPromotionAndBindRejection(t *testing.T) {
	handler := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) { _, _ = w.Write([]byte("ok")) })
	address := testAddress(t)
	manager, err := NewManager(testConfig(address, handler))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	first, err := manager.Replace(context.Background(), ReplaceRequest{Server: testConfig(address, handler)})
	if err != nil || first.State != ReplaceApplied {
		t.Fatalf("first replacement: %+v, %v", first, err)
	}
	before := manager.Current()
	changed := testConfig(address, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) { _, _ = w.Write([]byte("new")) }))
	changed.ProbeIdentity = "different-generation"
	second, err := manager.Replace(context.Background(), ReplaceRequest{Server: changed})
	if err == nil || second.State != ReplaceRejected {
		t.Fatalf("expected bind rejection, got %+v, %v", second, err)
	}
	after := manager.Current()
	if after.GenerationID != before.GenerationID || after.Address != before.Address {
		t.Fatalf("active generation changed after rejection: before=%+v after=%+v", before, after)
	}
}

func TestManagerSameConfigNoOp(t *testing.T) {
	address := testAddress(t)
	manager, err := NewManager(testConfig(address, nethttp.NotFoundHandler()))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	first, err := manager.Replace(context.Background(), ReplaceRequest{Server: testConfig(address, nethttp.NotFoundHandler())})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Replace(context.Background(), ReplaceRequest{Server: testConfig(address, nethttp.NotFoundHandler())})
	if err != nil {
		t.Fatal(err)
	}
	if second.State != ReplaceApplied || second.GenerationID != first.GenerationID {
		t.Fatalf("not a no-op: first=%+v second=%+v", first, second)
	}
}

type wrongRuntime struct{ server *nethttp.Server }

func (r wrongRuntime) Serve(listener net.Listener) error  { return r.server.Serve(listener) }
func (r wrongRuntime) Shutdown(ctx context.Context) error { return r.server.Shutdown(ctx) }
func (r wrongRuntime) Close() error                       { return r.server.Close() }

func TestManagerRejectsWrongProbeIdentity(t *testing.T) {
	address := testAddress(t)
	cfg := testConfig(address, nethttp.NotFoundHandler())
	cfg.RuntimeFactory = func(config ServerConfig, control RuntimeControl) (Runtime, error) {
		return wrongRuntime{server: &nethttp.Server{Handler: nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			if response, ok := control.Probe(r.Method, r.URL.Path, r.Header.Get("X-Hanami-Probe")); ok {
				w.Header().Set("X-Hanami-Probe-Identity", response.Identity)
				_, _ = w.Write([]byte("wrong"))
				return
			}
			w.WriteHeader(nethttp.StatusServiceUnavailable)
		})}}, nil
	}
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	result, err := manager.Replace(context.Background(), ReplaceRequest{Server: cfg})
	if err == nil || result.State != ReplaceRejected {
		t.Fatalf("expected identity rejection, got %+v, %v", result, err)
	}
	if manager.Current().Active {
		t.Fatal("rejected candidate became active")
	}
}

func TestManagerCancellationBeforePromotion(t *testing.T) {
	address := testAddress(t)
	manager, err := NewManager(testConfig(address, nethttp.NotFoundHandler()))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := manager.Replace(ctx, ReplaceRequest{Server: testConfig(address, nethttp.NotFoundHandler())})
	if !errors.Is(err, context.Canceled) || result.State != ReplaceRejected {
		t.Fatalf("unexpected cancellation result: %+v, %v", result, err)
	}
	if manager.Current().Active {
		t.Fatal("cancelled candidate became active")
	}
}

func TestManagerDrainFailureStillApplied(t *testing.T) {
	entered := make(chan struct{})
	block := make(chan struct{})
	var once sync.Once
	handler := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		once.Do(func() { close(entered) })
		<-block
	})
	oldAddress := testAddress(t)
	manager, err := NewManager(testConfig(oldAddress, handler))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(block); _ = manager.Shutdown(context.Background()) }()
	first, err := manager.Replace(context.Background(), ReplaceRequest{Server: testConfig(oldAddress, handler)})
	if err != nil || first.State != ReplaceApplied {
		t.Fatalf("initial replacement: %+v, %v", first, err)
	}
	client := &nethttp.Client{}
	go func() { _, _ = client.Get("http://" + manager.Address() + "/block") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("blocking request did not reach the old generation")
	}
	newCfg := testConfig(testAddress(t), nethttp.NotFoundHandler())
	newCfg.DrainTimeout = 20 * time.Millisecond
	second, err := manager.Replace(context.Background(), ReplaceRequest{Server: newCfg})
	if err == nil || second.State != ReplaceAppliedWithDrainFailure {
		t.Fatalf("expected applied drain failure: first=%+v second=%+v err=%v", first, second, err)
	}
	if manager.Current().GenerationID != second.GenerationID {
		t.Fatal("new generation was not retained after drain failure")
	}
}

func TestManagerDrainingLimit(t *testing.T) {
	entered := make(chan struct{})
	blocked := make(chan struct{})
	var once sync.Once
	blocking := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) { once.Do(func() { close(entered) }); <-blocked })
	firstAddr := testAddress(t)
	manager, err := NewManager(testConfig(firstAddr, blocking))
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { once.Do(func() { close(blocked) }); _ = manager.Shutdown(context.Background()) }
	defer cleanup()
	firstCfg := testConfig(firstAddr, blocking)
	firstCfg.MaxDrainingGenerations = 1
	if _, err := manager.Replace(context.Background(), ReplaceRequest{Server: firstCfg}); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = nethttp.Get("http://" + manager.Address() + "/block") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("blocking request did not start")
	}
	secondCfg := testConfig(testAddress(t), nethttp.NotFoundHandler())
	secondCfg.MaxDrainingGenerations = 1
	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Replace(context.Background(), ReplaceRequest{Server: secondCfg})
		secondDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for manager.Current().Draining == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.Current().Draining != 1 {
		t.Fatalf("drain did not become observable: %+v", manager.Current())
	}
	thirdCfg := testConfig(testAddress(t), nethttp.NotFoundHandler())
	thirdCfg.MaxDrainingGenerations = 1
	third, err := manager.Replace(context.Background(), ReplaceRequest{Server: thirdCfg})
	if err == nil || third.State != ReplaceRejected {
		t.Fatalf("expected draining limit rejection: %+v, %v", third, err)
	}
	if err := <-secondDone; err == nil {
		t.Fatal("expected second replacement drain timeout")
	}
	cleanup()
}

func TestManagerConcurrentReplacementSerialization(t *testing.T) {
	manager, err := NewManager(testConfig(testAddress(t), nethttp.NotFoundHandler()))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	var wg sync.WaitGroup
	results := make(chan ReplaceResult, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := manager.Replace(context.Background(), ReplaceRequest{Server: testConfig(testAddress(t), nethttp.NotFoundHandler())})
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !manager.Current().Active {
		t.Fatal("no active generation after concurrent replacements")
	}
}

func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
func TestManagerVerifiedTLSAndRotationMismatch(t *testing.T) {
	cert := testCertificate(t)
	cfg := testConfig("127.0.0.1:0", nethttp.NotFoundHandler())
	cfg.Protocol = ProtocolHTTPS
	cfg.Certificate = &cert
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	result, err := manager.Replace(context.Background(), ReplaceRequest{Server: cfg})
	if err != nil || result.State != ReplaceApplied {
		t.Fatalf("TLS promotion failed: %+v, %v", result, err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	client := &nethttp.Client{Transport: &nethttp.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "127.0.0.1"}}}
	response, err := client.Get("https://" + manager.Address() + "/")
	if err != nil {
		t.Fatalf("verified TLS request failed: %v", err)
	}
	_ = response.Body.Close()
	bad := testCertificate(t)
	bad.PrivateKey = cert.PrivateKey
	oldFingerprint := manager.Current().CertificateFingerprint
	if err := manager.RotateCertificate(bad); err == nil {
		t.Fatal("accepted mismatched certificate key")
	}
	if manager.Current().CertificateFingerprint != oldFingerprint {
		t.Fatal("mismatched rotation changed active certificate")
	}
}

type gatedRuntime struct{ server *nethttp.Server }

func (r gatedRuntime) Serve(listener net.Listener) error  { return r.server.Serve(listener) }
func (r gatedRuntime) Shutdown(ctx context.Context) error { return r.server.Shutdown(ctx) }
func (r gatedRuntime) Close() error                       { return r.server.Close() }

func TestCustomRuntimeUsesGenerationGate(t *testing.T) {
	address := testAddress(t)
	var control RuntimeControl
	cfg := testConfig(address, nethttp.NotFoundHandler())
	cfg.RuntimeFactory = func(config ServerConfig, gate RuntimeControl) (Runtime, error) {
		control = gate
		handler := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			if response, ok := gate.Probe(r.Method, r.URL.Path, r.Header.Get("X-Hanami-Probe")); ok {
				w.Header().Set("X-Hanami-Probe-Identity", response.Identity)
				w.WriteHeader(response.StatusCode)
				_, _ = w.Write([]byte(response.Body))
				return
			}
			if !gate.Admitted() {
				w.WriteHeader(nethttp.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("public"))
		})
		return gatedRuntime{server: &nethttp.Server{Handler: handler}}, nil
	}
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	result, err := manager.Replace(context.Background(), ReplaceRequest{Server: cfg})
	if err != nil || result.State != ReplaceApplied {
		t.Fatalf("promotion failed: %+v, %v", result, err)
	}
	if control == nil || !control.Admitted() {
		t.Fatal("generation gate did not admit after promotion")
	}
}

func TestGenerationGateBlocksPublicUntilPromotion(t *testing.T) {
	gate := newGenerationGate("/probe", "identity", "secret")
	if gate.Admitted() {
		t.Fatal("new generation gate is admitted")
	}
	if response, ok := gate.Probe(nethttp.MethodGet, "/probe", "secret"); !ok || response.StatusCode != nethttp.StatusOK || response.Identity != "identity" || response.Body != "identity" {
		t.Fatalf("authenticated probe failed before promotion: %+v, %v", response, ok)
	}
	if _, ok := gate.Probe(nethttp.MethodGet, "/probe", "wrong"); ok {
		t.Fatal("unauthenticated probe succeeded")
	}
	wrapped := gate.Wrap(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) { _, _ = w.Write([]byte("public")) }))
	recorder := httptest.NewRecorder()
	wrapped.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/public", nil))
	if recorder.Code != nethttp.StatusServiceUnavailable {
		t.Fatalf("public request before promotion returned %d", recorder.Code)
	}
	gate.setAdmitted(true)
	recorder = httptest.NewRecorder()
	wrapped.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/public", nil))
	if recorder.Code != nethttp.StatusOK || recorder.Body.String() != "public" {
		t.Fatalf("public request after promotion: %d %q", recorder.Code, recorder.Body.String())
	}
}
