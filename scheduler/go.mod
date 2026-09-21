module github.com/heavycaffeiner/hanami/scheduler

go 1.27.1

require (
	github.com/go-co-op/gocron/v2 v2.22.0
	github.com/google/uuid v1.6.0
	github.com/heavycaffeiner/hanami v0.0.0
	go.uber.org/fx v1.24.0
)

require (
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.26.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/heavycaffeiner/hanami => ..
