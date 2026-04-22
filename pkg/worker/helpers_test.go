package worker_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const scenarioYAMLTemplate = `apiVersion: edt.io/v1
kind: Scenario
metadata:
  name: %s
spec:
  connectors:
    kafka:
      bootstrap_servers: localhost:9092
  steps: []
`

func postScenario(baseURL, name string) error {
	body := fmt.Sprintf(scenarioYAMLTemplate, name)
	resp, err := http.Post(baseURL+"/api/v1/scenarios", "application/yaml", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("postScenario %d", resp.StatusCode)
	}
	return nil
}

func assignScenario(baseURL, workerID, scenario string) error {
	body, _ := json.Marshal(map[string]string{"scenario": scenario})
	resp, err := http.Post(baseURL+"/api/v1/workers/"+workerID+"/assignments", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("assignScenario %d", resp.StatusCode)
	}
	return nil
}
