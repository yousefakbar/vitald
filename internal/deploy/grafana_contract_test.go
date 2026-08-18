package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const grafanaDatasourceUID = "vitald-postgres"

func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return document
}

func TestGrafanaComposeAndSystemdContracts(t *testing.T) {
	root := projectRoot(t)
	compose := readYAML(t, filepath.Join(root, "compose.yaml"))
	services, ok := compose["services"].(map[string]any)
	if !ok {
		t.Fatal("Compose services are missing")
	}
	grafana, ok := services["grafana"].(map[string]any)
	if !ok {
		t.Fatal("Grafana Compose service is missing")
	}
	image, _ := grafana["image"].(string)
	if image == "" || strings.HasSuffix(image, ":latest") || !strings.Contains(image, ":") {
		t.Fatalf("Grafana image must have an explicit non-latest tag, got %q", image)
	}
	ports, ok := grafana["ports"].([]any)
	if !ok || len(ports) != 1 || ports[0] != "127.0.0.1:3107:3000" {
		t.Fatalf("Grafana must expose only 127.0.0.1:3107:3000, got %v", grafana["ports"])
	}
	volumes, ok := compose["volumes"].(map[string]any)
	if !ok {
		t.Fatal("Compose named volumes are missing")
	}
	if _, exists := volumes["grafana-data"]; !exists {
		t.Fatal("grafana-data named volume is required")
	}

	unitData, err := os.ReadFile(filepath.Join(root, "deploy/systemd/user/vitald-grafana.service.in"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(unitData)
	for _, required := range []string{
		"Requires=vitald-postgres.service",
		"After=vitald-postgres.service",
		"ExecStart=@VITALD_ROOT@/scripts/systemd-run.sh grafana-start",
		"ExecStop=@VITALD_ROOT@/scripts/systemd-run.sh grafana-stop",
	} {
		if !strings.Contains(unit, required) {
			t.Errorf("Grafana unit is missing %q", required)
		}
	}
}

func TestGrafanaProvisioningContracts(t *testing.T) {
	root := projectRoot(t)
	datasource := readYAML(t, filepath.Join(root, "deploy/grafana/provisioning/datasources/vitald.yaml"))
	datasources, ok := datasource["datasources"].([]any)
	if !ok || len(datasources) != 1 {
		t.Fatalf("expected exactly one provisioned datasource")
	}
	ds, ok := datasources[0].(map[string]any)
	if !ok {
		t.Fatal("datasource entry is not an object")
	}
	if ds["uid"] != grafanaDatasourceUID || ds["type"] != "postgres" || ds["editable"] != false {
		t.Fatalf("unexpected datasource contract: uid=%v type=%v editable=%v", ds["uid"], ds["type"], ds["editable"])
	}
	secure, ok := ds["secureJsonData"].(map[string]any)
	if !ok || secure["password"] != "$VITALD_GRAFANA_DB_PASSWORD" {
		t.Fatal("datasource password must come from VITALD_GRAFANA_DB_PASSWORD")
	}

	provider := readYAML(t, filepath.Join(root, "deploy/grafana/provisioning/dashboards/vitald.yaml"))
	providers, ok := provider["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("expected exactly one dashboard provider")
	}
	p, ok := providers[0].(map[string]any)
	if !ok || p["folderUid"] != "vitald" || p["type"] != "file" {
		t.Fatalf("unexpected dashboard provider contract")
	}
	options, ok := p["options"].(map[string]any)
	if !ok || options["path"] != "/etc/grafana/dashboards" {
		t.Fatal("unexpected provisioned dashboard path")
	}
}

var (
	relationPattern = regexp.MustCompile(`(?i)\b(?:from|join)\s+"?([a-z_][a-z0-9_]*)(?:"?\."?([a-z_][a-z0-9_]*))?`)
	secretPattern   = regexp.MustCompile(`(?i)(password\s*=|postgres(?:ql)?://[^\s"]+:[^\s"]+@|GRAFANA_ADMIN_PASSWORD|VITALD_GRAFANA_DB_PASSWORD)`)
)

func TestGrafanaDashboardContracts(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(projectRoot(t), "deploy/grafana/dashboards/*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 5 {
		t.Fatalf("expected five dashboards, found %d", len(paths))
	}

	uids := make(map[string]string)
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if secretPattern.Match(data) {
				t.Fatal("dashboard contains a credential or secret-variable reference")
			}

			var dashboard map[string]any
			if err := json.Unmarshal(data, &dashboard); err != nil {
				t.Fatalf("invalid dashboard JSON: %v", err)
			}
			uid, _ := dashboard["uid"].(string)
			if uid == "" {
				t.Fatal("dashboard UID is required")
			}
			if previous, exists := uids[uid]; exists {
				t.Fatalf("dashboard UID %q is also used by %s", uid, previous)
			}
			uids[uid] = filepath.Base(path)
			if dashboard["timezone"] != "Asia/Riyadh" {
				t.Fatalf("timezone must be Asia/Riyadh, got %v", dashboard["timezone"])
			}

			analyticsReferences := 0
			walkJSON(t, dashboard, func(object map[string]any) {
				if dsType, _ := object["type"].(string); dsType == "grafana-postgresql-datasource" {
					if object["uid"] != grafanaDatasourceUID {
						t.Errorf("PostgreSQL datasource UID must be %s, got %v", grafanaDatasourceUID, object["uid"])
					}
				}
				rawSQL, ok := object["rawSql"].(string)
				if !ok || rawSQL == "" {
					return
				}
				for _, match := range relationPattern.FindAllStringSubmatch(rawSQL, -1) {
					schema, relation := strings.ToLower(match[1]), strings.ToLower(match[2])
					if relation != "" {
						if schema != "analytics" {
							t.Errorf("SQL references non-analytics object %s.%s", schema, relation)
						} else {
							analyticsReferences++
						}
					} else if schema == "health_records" || strings.HasPrefix(schema, "sync_") || schema == "raw_archives" {
						t.Errorf("SQL references internal relation %s", schema)
					}
				}
			})
			if analyticsReferences == 0 {
				t.Fatal("dashboard must query at least one analytics view")
			}
		})
	}
}

func walkJSON(t *testing.T, value any, visit func(map[string]any)) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		visit(value)
		for _, child := range value {
			walkJSON(t, child, visit)
		}
	case []any:
		for _, child := range value {
			walkJSON(t, child, visit)
		}
	}
}
