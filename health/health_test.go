package health

import (
	"context"
	"testing"

	"github.com/heavycaffeiner/hanami/bootstrap"
)

func TestReadinessIncludesAdmissionState(t *testing.T) {
	admission := bootstrap.NewAdmission()
	registry, err := New(Params{}, admission)
	if err != nil {
		t.Fatal(err)
	}
	if report := registry.Check(context.Background(), Readiness, false); report.Status != "fail" {
		t.Fatalf("closed admission reported %s", report.Status)
	}
	admission.Open()
	if report := registry.Check(context.Background(), Readiness, false); report.Status != "pass" {
		t.Fatalf("open admission reported %s", report.Status)
	}
}
