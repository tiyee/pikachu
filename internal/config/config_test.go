package config

import (
	"os"
	"path/filepath"
	"pikachu/internal/types"
	"strings"
	"testing"
	"time"
)

func validConfig() *types.Config {
	return &types.Config{Database: types.DatabaseConfig{Host: "localhost", Port: 3306, User: "u", Database: "db", ServerID: 1}, Tasks: []types.Task{{TaskID: "one", TableName: "users", Events: []types.EventType{types.EventInsert}, CallbackURL: "http://localhost/webhook"}}}
}

func TestStrictLoadingAndRetryDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		want        int
		fail        bool
	}{
		{"omitted", "", 3, false}, {"disabled", "dispatcher:\n  max_retries: 0\n", 0, false},
		{"unknown", "dispatcher:\n  worker_cout: 3\n", 0, true}, {"multiple", "---\nlog: {}\n", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			f := filepath.Join(d, "config.yaml")
			data := "tasks:\n  - task_id: one\n" + tc.extra
			if err := os.WriteFile(f, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := LoadConfig(f, filepath.Join(d, "missing.yaml"))
			if tc.fail {
				if err == nil {
					t.Fatal("invalid YAML accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Dispatcher.MaxRetries != tc.want {
				t.Fatalf("retries=%d want=%d", c.Dispatcher.MaxRetries, tc.want)
			}
		})
	}
}

func TestTasksErrorsAndDefaultPath(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "config.yaml")
	tasks := filepath.Join(d, "tasks.yaml")
	if err := os.WriteFile(f, []byte("tasks:\n  - task_id: inline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"tasks: [", "tasks:\n  - task_id: new\n    callback_ur: /x\n"} {
		if err := os.WriteFile(tasks, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(f, tasks); err == nil {
			t.Fatal("task parsing error ignored")
		}
	}
	if _, err := LoadConfig(f, d); err == nil {
		t.Fatal("task read error ignored")
	}
	if err := os.WriteFile(tasks, []byte("tasks:\n  - task_id: external\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(d)
	c, err := LoadConfig(f, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Tasks[0].TaskID != "external" {
		t.Fatal("default tasks path not loaded")
	}
}

func TestFinalCallbackURLValidation(t *testing.T) {
	for _, tc := range []struct {
		host, url string
		valid     bool
	}{
		{"", "/webhook", false}, {"http://localhost/api", "/webhook", true},
		{"ftp://localhost", "/webhook", false}, {"localhost", "/webhook", false},
		{"http://localhost?token=x", "/webhook", false}, {"http://localhost", "//other/webhook", false},
		{"", "http://localhost:99999/x", false}, {"", "http://:80/x", false},
		{"", "http://u:p@localhost/x", false}, {"", "https://localhost/x", true},
	} {
		c := validConfig()
		c.CallbackHost = tc.host
		c.Tasks[0].CallbackURL = tc.url
		if err := ValidateConfig(c); (err == nil) != tc.valid {
			t.Errorf("host=%q URL=%q: %v", tc.host, tc.url, err)
		}
	}
}

func TestValidationRejectsUnsupportedSettings(t *testing.T) {
	for _, change := range []func(*types.Config){
		func(c *types.Config) { c.Dispatcher.MaxRetries = -1 }, func(c *types.Config) { c.Dispatcher.WorkerCount = -1 },
		func(c *types.Config) { c.Database.ReadTimeout = -time.Second }, func(c *types.Config) { c.Log.Format = "jsno" },
		func(c *types.Config) { c.Log.Level = "invalid" }, func(c *types.Config) { c.Log.MaxSize = -1 },
		func(c *types.Config) { c.Server.Enabled = true; c.Server.Port = 70000 },
		func(c *types.Config) { c.Server.Enabled = true; c.Server.Path = "/metrics-json" },
		func(c *types.Config) { c.Dispatcher.BatchSize = 2 }, func(c *types.Config) { c.Monitor.FlushInterval = 2 * time.Second },
	} {
		c := validConfig()
		change(c)
		if err := ValidateConfig(c); err == nil {
			t.Fatalf("invalid config accepted: %+v", c)
		}
	}
	c := validConfig()
	c.Tasks[0].Events = []types.EventType{types.EventInsert, types.EventInsert}
	c.Dispatcher.MaxRetries = 0
	if err := ValidateConfig(c); err != nil {
		t.Fatal(err)
	}
	if len(c.Tasks[0].Events) != 1 || c.Dispatcher.MaxRetries != 0 {
		t.Fatal("duplicates or explicit zero changed incorrectly")
	}
}

func TestEnvironmentExamples(t *testing.T) {
	for _, name := range []string{"config-example.yaml", "config.prod.yaml", "config.test.yaml"} {
		c, err := LoadConfig(filepath.Join("../..", name), "../../tasks-example.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateConfig(c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
func TestDuplicateTaskIDRejected(t *testing.T) {
	cfg := validConfig()
	cfg.Tasks = append(cfg.Tasks, cfg.Tasks[0])
	cfg.Tasks[1].CallbackURL = "http://localhost/other"
	if err := ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "duplicate task_id") {
		t.Fatalf("expected duplicate ID error: %v", err)
	}
	cfg.Tasks[1].TaskID = "two"
	if err := ValidateConfig(cfg); err != nil {
		t.Fatal(err)
	}
}
func TestDeliveryDefaultsAndValidation(t *testing.T) {
	cfg := validConfig()
	if err := ValidateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Dispatcher.ShutdownTimeout != 30*time.Second {
		t.Fatal("missing delivery defaults")
	}
	cfg.Dispatcher.ShutdownTimeout = -time.Second
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("negative shutdown timeout accepted")
	}
}
