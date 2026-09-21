package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	gingonic "github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

type greetingService struct{}

func (greetingService) Greeting(ctx context.Context, name string) string {
	if ctx == nil {
		return ""
	}
	return "hello " + name
}

type greetingOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func TestModuleProvidesNativeHumaAPIWithValidationAndOpenAPI(t *testing.T) {
	ginMode := gingonic.Mode()
	gingonic.SetMode(gingonic.TestMode)
	t.Cleanup(func() { gingonic.SetMode(ginMode) })

	engine := gingonic.New()
	config := huma.DefaultConfig("Greeting API", "1.0.0")
	config.OpenAPIPath = "/openapi"
	config.DocsPath = ""

	var registered huma.API
	app := fx.New(
		fx.Supply(engine, greetingService{}),
		Module(Config{Huma: config, Prefix: "v1"}),
		fx.Invoke(func(api huma.API, service greetingService) {
			registered = api
			huma.Get(api, "/greetings", func(ctx context.Context, input *struct {
				Name string `query:"name" minLength:"3"`
			}) (*greetingOutput, error) {
				output := &greetingOutput{}
				output.Body.Message = service.Greeting(ctx, input.Name)
				return output, nil
			})
		}),
	)
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Stop(ctx) })
	if registered == nil {
		t.Fatal("Fx did not provide the native huma.API")
	}

	t.Run("validation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/greetings?name=x", nil)
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusUnprocessableEntity)
		}
	})

	t.Run("response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/greetings?name=world", nil)
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
		}
		var body struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Message != "hello world" {
			t.Fatalf("message = %q, want %q", body.Message, "hello world")
		}
	})

	t.Run("openapi", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
		}
		var document struct {
			OpenAPI string         `json:"openapi"`
			Paths   map[string]any `json:"paths"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if document.OpenAPI != "3.1.0" {
			t.Fatalf("openapi version = %q, want %q", document.OpenAPI, "3.1.0")
		}
		if _, ok := document.Paths["/greetings"]; !ok {
			t.Fatal("OpenAPI document omitted /greetings")
		}
	})
}
