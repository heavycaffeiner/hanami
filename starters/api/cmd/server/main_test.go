package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/heavycaffeiner/hanami/bootstrap"
	"github.com/heavycaffeiner/hanami/health"
)

func TestGreetingRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admission := bootstrap.NewAdmission()
	admission.Open()
	registry, err := health.New(health.Params{}, admission)
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	registerRoutes(engine, registry, slog.Default())

	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/v1/greeting", nil).WithContext(context.Background())
	engine.ServeHTTP(response, request)

	if response.Code != 200 {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "hello from Hanami" {
		t.Fatalf("unexpected message: %s", body.Message)
	}
}
