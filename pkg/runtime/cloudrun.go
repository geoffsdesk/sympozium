// Package runtime provides alternative execution backends for Sympozium agent runs.
// The default is GKE Jobs, but Cloud Run Jobs offers a serverless alternative
// with faster cold starts and no cluster capacity planning.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexsjones/sympozium/pkg/gcpauth"
)

// RuntimeType defines the agent execution backend.
type RuntimeType string

const (
	// RuntimeGKE runs agents as Kubernetes Jobs on GKE (default).
	RuntimeGKE RuntimeType = "gke"

	// RuntimeCloudRun runs agents as Cloud Run Jobs (serverless).
	RuntimeCloudRun RuntimeType = "cloudrun"
)

// CloudRunJobConfig configures a Cloud Run Job execution.
type CloudRunJobConfig struct {
	// ProjectID is the GCP project.
	ProjectID string `json:"projectId"`

	// Location is the GCP region (e.g., "us-central1").
	Location string `json:"location"`

	// Image is the container image to run.
	Image string `json:"image"`

	// ServiceAccount is the IAM service account for the job.
	ServiceAccount string `json:"serviceAccount,omitempty"`

	// Timeout is the maximum execution duration.
	Timeout time.Duration `json:"timeout,omitempty"`

	// Memory is the memory allocation (e.g., "512Mi", "1Gi").
	Memory string `json:"memory,omitempty"`

	// CPU is the CPU allocation (e.g., "1", "2").
	CPU string `json:"cpu,omitempty"`

	// EnvVars contains environment variables for the container.
	EnvVars map[string]string `json:"envVars,omitempty"`

	// VPCConnector is the Serverless VPC Access connector for private networking.
	VPCConnector string `json:"vpcConnector,omitempty"`
}

// CloudRunExecutor executes agent runs as Cloud Run Jobs.
type CloudRunExecutor struct {
	tokenSrc  *gcpauth.CacheableTokenSource
	client    *http.Client
	projectID string
	location  string
}

// NewCloudRunExecutor creates a new Cloud Run executor.
func NewCloudRunExecutor(tokenSrc *gcpauth.CacheableTokenSource, projectID, location string) *CloudRunExecutor {
	if location == "" {
		location = "us-central1"
	}
	return &CloudRunExecutor{
		tokenSrc:  tokenSrc,
		client:    &http.Client{Timeout: 2 * time.Minute},
		projectID: projectID,
		location:  location,
	}
}

// CloudRunJob represents a Cloud Run Job definition.
type CloudRunJob struct {
	Name     string `json:"name"`
	UID      string `json:"uid,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Template CloudRunJobTemplate `json:"template"`
}

// CloudRunJobTemplate defines the job execution template.
type CloudRunJobTemplate struct {
	Template CloudRunTaskTemplate `json:"template"`
}

// CloudRunTaskTemplate defines the task configuration.
type CloudRunTaskTemplate struct {
	Containers     []CloudRunContainer `json:"containers"`
	ServiceAccount string              `json:"serviceAccount,omitempty"`
	Timeout        string              `json:"timeout,omitempty"`
	MaxRetries     int                 `json:"maxRetries,omitempty"`
	VPCAccess      *VPCAccess          `json:"vpcAccess,omitempty"`
}

// CloudRunContainer defines a container in a Cloud Run Job.
type CloudRunContainer struct {
	Image     string           `json:"image"`
	Env       []CloudRunEnvVar `json:"env,omitempty"`
	Resources *CloudRunResources `json:"resources,omitempty"`
}

// CloudRunEnvVar is an environment variable.
type CloudRunEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	// ValueSource can reference Secret Manager
	ValueSource *EnvVarSource `json:"valueSource,omitempty"`
}

// EnvVarSource references a secret version.
type EnvVarSource struct {
	SecretKeyRef *SecretKeyRef `json:"secretKeyRef,omitempty"`
}

// SecretKeyRef references a Secret Manager secret.
type SecretKeyRef struct {
	Secret  string `json:"secret"`
	Version string `json:"version"`
}

// CloudRunResources defines resource limits.
type CloudRunResources struct {
	Limits map[string]string `json:"limits,omitempty"`
}

// VPCAccess configures VPC connectivity.
type VPCAccess struct {
	Connector string `json:"connector,omitempty"`
	Egress    string `json:"egress,omitempty"` // ALL_TRAFFIC, PRIVATE_RANGES_ONLY
}

// CreateAndRunJob creates a Cloud Run Job and immediately executes it.
func (cr *CloudRunExecutor) CreateAndRunJob(ctx context.Context, config CloudRunJobConfig) (string, error) {
	jobName := fmt.Sprintf("sympozium-agent-%d", time.Now().UnixMilli())

	// Build environment variables
	var envVars []CloudRunEnvVar
	for k, v := range config.EnvVars {
		envVars = append(envVars, CloudRunEnvVar{Name: k, Value: v})
	}

	// Build resource limits
	limits := map[string]string{}
	if config.Memory != "" {
		limits["memory"] = config.Memory
	} else {
		limits["memory"] = "512Mi"
	}
	if config.CPU != "" {
		limits["cpu"] = config.CPU
	} else {
		limits["cpu"] = "1"
	}

	timeout := "600s" // 10 minutes default
	if config.Timeout > 0 {
		timeout = fmt.Sprintf("%ds", int(config.Timeout.Seconds()))
	}

	job := CloudRunJob{
		Name: jobName,
		Labels: map[string]string{
			"sympozium.ai/component": "agent-runner",
			"managed-by":            "sympozium",
		},
		Template: CloudRunJobTemplate{
			Template: CloudRunTaskTemplate{
				Containers: []CloudRunContainer{{
					Image:     config.Image,
					Env:       envVars,
					Resources: &CloudRunResources{Limits: limits},
				}},
				ServiceAccount: config.ServiceAccount,
				Timeout:        timeout,
				MaxRetries:     0,
			},
		},
	}

	if config.VPCConnector != "" {
		job.Template.Template.VPCAccess = &VPCAccess{
			Connector: config.VPCConnector,
			Egress:    "ALL_TRAFFIC",
		}
	}

	// Create the job via Cloud Run Admin API
	location := config.Location
	if location == "" {
		location = cr.location
	}
	projectID := config.ProjectID
	if projectID == "" {
		projectID = cr.projectID
	}

	apiURL := fmt.Sprintf(
		"https://run.googleapis.com/v2/projects/%s/locations/%s/jobs?jobId=%s",
		projectID, location, jobName,
	)

	body, _ := json.Marshal(job)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	token, err := cr.tokenSrc.GetAccessToken()
	if err != nil {
		return "", fmt.Errorf("getting access token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := cr.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("creating Cloud Run job: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errBody map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return "", fmt.Errorf("Cloud Run API error %d: %v", resp.StatusCode, errBody)
	}

	// Now run the job
	runURL := fmt.Sprintf(
		"https://run.googleapis.com/v2/projects/%s/locations/%s/jobs/%s:run",
		projectID, location, jobName,
	)

	runReq, err := http.NewRequestWithContext(ctx, http.MethodPost, runURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating run request: %w", err)
	}
	runReq.Header.Set("Authorization", "Bearer "+token)

	runResp, err := cr.client.Do(runReq)
	if err != nil {
		return "", fmt.Errorf("running Cloud Run job: %w", err)
	}
	defer runResp.Body.Close()

	if runResp.StatusCode >= 400 {
		var errBody map[string]interface{}
		_ = json.NewDecoder(runResp.Body).Decode(&errBody)
		return "", fmt.Errorf("Cloud Run run error %d: %v", runResp.StatusCode, errBody)
	}

	return jobName, nil
}

// GetJobStatus checks the status of a Cloud Run Job execution.
func (cr *CloudRunExecutor) GetJobStatus(ctx context.Context, jobName string) (string, error) {
	apiURL := fmt.Sprintf(
		"https://run.googleapis.com/v2/projects/%s/locations/%s/jobs/%s/executions",
		cr.projectID, cr.location, jobName,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}

	token, err := cr.tokenSrc.GetAccessToken()
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := cr.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Executions []struct {
			CompletionTime string `json:"completionTime"`
			Conditions     []struct {
				Type   string `json:"type"`
				State  string `json:"state"`
				Reason string `json:"reason"`
			} `json:"conditions"`
		} `json:"executions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Executions) == 0 {
		return "PENDING", nil
	}

	exec := result.Executions[0]
	for _, cond := range exec.Conditions {
		if cond.Type == "Completed" && cond.State == "CONDITION_SUCCEEDED" {
			return "COMPLETED", nil
		}
		if cond.State == "CONDITION_FAILED" {
			return "FAILED", nil
		}
	}
	return "RUNNING", nil
}

// DeleteJob cleans up a Cloud Run Job after completion.
func (cr *CloudRunExecutor) DeleteJob(ctx context.Context, jobName string) error {
	apiURL := fmt.Sprintf(
		"https://run.googleapis.com/v2/projects/%s/locations/%s/jobs/%s",
		cr.projectID, cr.location, jobName,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return err
	}

	token, err := cr.tokenSrc.GetAccessToken()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := cr.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
