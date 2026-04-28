// GitLab Runners: запрашиваем список через GraphQL (один POST со всеми
// полями), c REST-fallback'ом и 60s кешем. Авторизация через PRIVATE-TOKEN —
// либо из флагов/env, либо автоопределение из конфига glab CLI.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/yaml"
)

// ========== GitLab client interface ==========

// GitLabClient абстрагирует транспорт к GitLab API.
// Реальная реализация — gitLabHTTPClient; в тестах подменяется моком.
type GitLabClient interface {
	Get(ctx context.Context, path string) (*http.Response, error)
	Post(ctx context.Context, path string) (*http.Response, error)
	GraphQL(ctx context.Context, query string) ([]byte, error)
}

type gitLabHTTPClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newGitLabClient(baseURL, token string) GitLabClient {
	return &gitLabHTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *gitLabHTTPClient) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	return c.http.Do(req)
}

func (c *gitLabHTTPClient) Post(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	return c.http.Do(req)
}

func (c *gitLabHTTPClient) GraphQL(ctx context.Context, query string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/graphql",
		strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graphql status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ========== /api/runners ==========

type RunnerItem struct {
	ID          int      `json:"id"`
	Description string   `json:"description"`
	Status      string   `json:"status"` // online | offline | stale | never_contacted
	Paused      bool     `json:"paused"`
	IsShared    bool     `json:"isShared"`
	RunnerType  string   `json:"runnerType"` // instance_type | group_type | project_type
	Tags        []string `json:"tags"`
	ContactedAt string   `json:"contactedAt"` // RFC3339
	WebURL      string   `json:"webURL"`
	Executor    string   `json:"executor"` // docker | shell | kubernetes | ssh
	Hostname    string   `json:"hostname"` // machine name from runner --name
}

// runnerCache: одноминутный кеш на ответ /api/runners — снимает нагрузку с
// GitLab при частых рефрешах фронта.
type runnerCache struct {
	mu              sync.Mutex
	expires         time.Time
	payload         []RunnerItem
	graphQLDisabled bool // выставляется при первой permission-ошибке, защищён mu
}

var runnersCache runnerCache

const runnersCacheTTL = 60 * time.Second

func handleRunners(gl GitLabClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runnersCache.mu.Lock()
		if time.Now().Before(runnersCache.expires) && runnersCache.payload != nil {
			payload := runnersCache.payload
			runnersCache.mu.Unlock()
			writeJSON(w, payload)
			return
		}
		graphQLDisabled := runnersCache.graphQLDisabled
		runnersCache.mu.Unlock()

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		var out []RunnerItem
		if !graphQLDisabled {
			out = fetchRunnersGraphQL(ctx, gl)
		}
		if out == nil {
			out = fetchRunnersREST(ctx, gl)
		}
		if out == nil {
			writeJSON(w, []RunnerItem{})
			return
		}
		enrichRunnersDetail(ctx, gl, out)

		runnersCache.mu.Lock()
		runnersCache.payload = out
		runnersCache.expires = time.Now().Add(runnersCacheTTL)
		runnersCache.mu.Unlock()

		writeJSON(w, out)
	}
}

// parseTagList разбирает поле tagList, которое разные версии GitLab GraphQL
// отдают по-разному: либо как JSON-массив ["docker","linux"], либо как строку
// "docker,linux". В обоих случаях возвращает []string, никогда не nil.
func parseTagList(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{}
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		if arr == nil {
			return []string{}
		}
		return arr
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	return []string{}
}

// fetchRunnersGraphQL — один запрос со всеми полями. GraphQL ID вида
// "gid://gitlab/Ci::Runner/123" — извлекаем числовой суффикс для webURL.
func fetchRunnersGraphQL(ctx context.Context, gl GitLabClient) []RunnerItem {
	q := `{
		runners(first: 100) {
			nodes {
				id
				description
				status
				paused
				runnerType
				tagList
				contactedAt
			}
		}
	}`
	raw, err := gl.GraphQL(ctx, q)
	if err != nil {
		log.Printf("gitlab runners graphql: %v (fallback to REST)", err)
		return nil
	}
	var resp struct {
		Data struct {
			Runners struct {
				Nodes []struct {
					ID          string          `json:"id"`
					Description string          `json:"description"`
					Status      string          `json:"status"`
					Paused      bool            `json:"paused"`
					RunnerType  string          `json:"runnerType"`
					TagList     json.RawMessage `json:"tagList"`
					ContactedAt string          `json:"contactedAt"`
				} `json:"nodes"`
			} `json:"runners"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		log.Printf("gitlab runners graphql: decode: %v", err)
		return nil
	}
	if len(resp.Errors) > 0 {
		msg := resp.Errors[0].Message
		log.Printf("gitlab runners graphql: %s — switching to REST permanently", msg)
		runnersCache.mu.Lock()
		runnersCache.graphQLDisabled = true
		runnersCache.mu.Unlock()
		return nil
	}
	out := make([]RunnerItem, 0, len(resp.Data.Runners.Nodes))
	for _, n := range resp.Data.Runners.Nodes {
		idNum := 0
		if i := strings.LastIndex(n.ID, "/"); i >= 0 {
			fmt.Sscanf(n.ID[i+1:], "%d", &idNum)
		}
		status := strings.ToLower(n.Status)
		rtype := strings.ToLower(n.RunnerType)
		tags := parseTagList(n.TagList)
		out = append(out, RunnerItem{
			ID:          idNum,
			Description: n.Description,
			Status:      status,
			Paused:      n.Paused,
			RunnerType:  rtype,
			IsShared:    rtype == "instance_type",
			Tags:        tags,
			ContactedAt: n.ContactedAt,
		})
	}
	return out
}

// enrichRunnersDetail обогащает список раннеров данными из /api/v4/runners/:id
// (contactedAt, tags, executor, hostname). Вызывается для обоих путей — GraphQL
// и REST — потому что list-endpoint и GraphQL могут не отдавать эти поля.
func enrichRunnersDetail(ctx context.Context, gl GitLabClient, runners []RunnerItem) {
	type det struct {
		ContactedAt  string   `json:"contacted_at"`
		TagList      []string `json:"tag_list"`
		ExecutorName string   `json:"executor_type"`
		Name         string   `json:"name"`
		WebURL       string   `json:"web_url"`
	}
	dets := make([]det, len(runners))
	var wg sync.WaitGroup
	for i, r := range runners {
		wg.Add(1)
		go func(i, id int) {
			defer wg.Done()
			resp, err := gl.Get(ctx, fmt.Sprintf("/api/v4/runners/%d", id))
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}
			_ = json.NewDecoder(resp.Body).Decode(&dets[i])
		}(i, r.ID)
	}
	wg.Wait()

	for i := range runners {
		if dets[i].ContactedAt != "" {
			runners[i].ContactedAt = dets[i].ContactedAt
		}
		if len(dets[i].TagList) > 0 {
			runners[i].Tags = dets[i].TagList
		}
		if dets[i].ExecutorName != "" {
			runners[i].Executor = dets[i].ExecutorName
		}
		if dets[i].Name != "" {
			runners[i].Hostname = dets[i].Name
		}
		if dets[i].WebURL != "" {
			runners[i].WebURL = dets[i].WebURL
		}
	}
}

// fetchRunnersREST — fallback если GraphQL вернул ошибку.
func fetchRunnersREST(ctx context.Context, gl GitLabClient) []RunnerItem {
	resp, err := gl.Get(ctx, "/api/v4/runners/all?per_page=100")
	if err != nil {
		log.Printf("gitlab runners rest: %v", err)
		return nil
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		resp, err = gl.Get(ctx, "/api/v4/runners?per_page=100")
		if err != nil {
			log.Printf("gitlab runners rest fallback: %v", err)
			return nil
		}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		log.Printf("gitlab runners rest: status %d", resp.StatusCode)
		return nil
	}
	defer resp.Body.Close()

	var list []struct {
		ID          int      `json:"id"`
		Description string   `json:"description"`
		Status      string   `json:"status"`
		Paused      bool     `json:"paused"`
		IsShared    bool     `json:"is_shared"`
		RunnerType  string   `json:"runner_type"`
		TagList     []string `json:"tag_list"`
		ContactedAt string   `json:"contacted_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		log.Printf("gitlab runners rest: decode: %v", err)
		return nil
	}
	out := make([]RunnerItem, 0, len(list))
	for _, it := range list {
		tags := it.TagList
		if tags == nil {
			tags = []string{}
		}
		out = append(out, RunnerItem{
			ID:          it.ID,
			Description: it.Description,
			Status:      it.Status,
			Paused:      it.Paused,
			IsShared:    it.IsShared,
			RunnerType:  it.RunnerType,
			Tags:        tags,
			ContactedAt: it.ContactedAt,
		})
	}
	return out
}

// ========== /api/pipelines ==========

type PipelineItem struct {
	ID        int    `json:"id"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	WebURL    string `json:"webURL"`
	Duration  int    `json:"duration"`
	User      string `json:"user"`
}

type PipelineProject struct {
	ID        int            `json:"id"`
	Name      string         `json:"name"`
	FullPath  string         `json:"fullPath"`
	WebURL    string         `json:"webURL"`
	Pipelines []PipelineItem `json:"pipelines"`
}

type plCacheState struct {
	mu      sync.Mutex
	expires time.Time
	payload []PipelineProject
}

var pipelinesCache plCacheState

const pipelinesCacheTTL = 30 * time.Second

func handlePipelines(gl GitLabClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pipelinesCache.mu.Lock()
		if time.Now().Before(pipelinesCache.expires) && pipelinesCache.payload != nil {
			payload := pipelinesCache.payload
			pipelinesCache.mu.Unlock()
			writeJSON(w, payload)
			return
		}
		pipelinesCache.mu.Unlock()

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		out := fetchPipelinesGraphQL(ctx, gl)
		if out == nil {
			writeJSON(w, []PipelineProject{})
			return
		}

		pipelinesCache.mu.Lock()
		pipelinesCache.payload = out
		pipelinesCache.expires = time.Now().Add(pipelinesCacheTTL)
		pipelinesCache.mu.Unlock()

		writeJSON(w, out)
	}
}

func fetchPipelinesGraphQL(ctx context.Context, gl GitLabClient) []PipelineProject {
	q := `{
		projects(membership: true, first: 30) {
			nodes {
				id
				name
				fullPath
				webUrl
				pipelines(first: 5) {
					nodes {
						id
						status
						ref
						sha
						createdAt
						updatedAt
						duration
						user { username }
					}
				}
			}
		}
	}`
	raw, err := gl.GraphQL(ctx, q)
	if err != nil {
		log.Printf("gitlab pipelines graphql: %v", err)
		return nil
	}

	var resp struct {
		Data struct {
			Projects struct {
				Nodes []struct {
					ID        string `json:"id"`
					Name      string `json:"name"`
					FullPath  string `json:"fullPath"`
					WebURL    string `json:"webUrl"`
					Pipelines struct {
						Nodes []struct {
							ID        string `json:"id"`
							Status    string `json:"status"`
							Ref       string `json:"ref"`
							SHA       string `json:"sha"`
							CreatedAt string `json:"createdAt"`
							UpdatedAt string `json:"updatedAt"`
							Duration  *int   `json:"duration"`
							User      *struct {
								Username string `json:"username"`
							} `json:"user"`
						} `json:"nodes"`
					} `json:"pipelines"`
				} `json:"nodes"`
			} `json:"projects"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(raw, &resp); err != nil {
		log.Printf("gitlab pipelines graphql: decode: %v", err)
		return nil
	}
	if len(resp.Errors) > 0 {
		log.Printf("gitlab pipelines graphql: %s", resp.Errors[0].Message)
		return nil
	}

	extractID := func(gid string) int {
		n := 0
		if i := strings.LastIndex(gid, "/"); i >= 0 {
			fmt.Sscanf(gid[i+1:], "%d", &n)
		}
		return n
	}

	var out []PipelineProject
	for _, proj := range resp.Data.Projects.Nodes {
		if len(proj.Pipelines.Nodes) == 0 {
			continue
		}
		pp := PipelineProject{
			ID:       extractID(proj.ID),
			Name:     proj.Name,
			FullPath: proj.FullPath,
			WebURL:   proj.WebURL,
		}
		for _, pl := range proj.Pipelines.Nodes {
			dur := 0
			if pl.Duration != nil {
				dur = *pl.Duration
			}
			user := ""
			if pl.User != nil {
				user = pl.User.Username
			}
			plID := extractID(pl.ID)
			pp.Pipelines = append(pp.Pipelines, PipelineItem{
				ID:        plID,
				Status:    strings.ToLower(pl.Status),
				Ref:       pl.Ref,
				SHA:       pl.SHA,
				CreatedAt: pl.CreatedAt,
				UpdatedAt: pl.UpdatedAt,
				WebURL:    fmt.Sprintf("%s/-/pipelines/%d", strings.TrimRight(proj.WebURL, "/"), plID),
				Duration:  dur,
				User:      user,
			})
		}
		out = append(out, pp)
	}
	return out
}

// handlePipelineAction — retry/cancel через glab CLI с fallback на GitLab REST API.
func handlePipelineAction(gl GitLabClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Action     string `json:"action"`
			ProjectID  int    `json:"projectId"`
			PipelineID int    `json:"pipelineId"`
			FullPath   string `json:"fullPath"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Action != "retry" && req.Action != "cancel" {
			http.Error(w, "unknown action", http.StatusBadRequest)
			return
		}

		err := runGlabPipelineAction(req.Action, req.FullPath, req.PipelineID)
		if err != nil {
			log.Printf("glab pipeline %s #%d: %v — falling back to REST", req.Action, req.PipelineID, err)
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			path := fmt.Sprintf("/api/v4/projects/%d/pipelines/%d/%s",
				req.ProjectID, req.PipelineID, req.Action)
			if err2 := restPipelineAction(ctx, gl, path); err2 != nil {
				http.Error(w, err2.Error(), http.StatusBadGateway)
				return
			}
		}

		pipelinesCache.mu.Lock()
		pipelinesCache.expires = time.Time{}
		pipelinesCache.mu.Unlock()

		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func runGlabPipelineAction(action, fullPath string, pipelineID int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "glab", "pipeline", action,
		fmt.Sprintf("%d", pipelineID), "-R", fullPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func restPipelineAction(ctx context.Context, gl GitLabClient, path string) error {
	resp, err := gl.Post(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("REST pipeline action: status %d", resp.StatusCode)
	}
	return nil
}

// ========== /api/pipelines/trends ==========

type PipelineTrendItem struct {
	ID        int    `json:"id"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	CreatedAt string `json:"createdAt"`
	Duration  int    `json:"duration"`
	WebURL    string `json:"webURL"`
}

func handlePipelineTrends(gl GitLabClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.URL.Query().Get("project")
		if projectID == "" {
			http.Error(w, "project required", http.StatusBadRequest)
			return
		}
		since := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02T15:04:05Z")
		path := fmt.Sprintf("/api/v4/projects/%s/pipelines?per_page=100&updated_after=%s&order_by=updated_at&sort=asc",
			projectID, since)

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		resp, err := gl.Get(ctx, path)
		if err != nil {
			log.Printf("pipeline trends %s: %v", projectID, err)
			writeJSON(w, []PipelineTrendItem{})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("pipeline trends %s: status %d", projectID, resp.StatusCode)
			writeJSON(w, []PipelineTrendItem{})
			return
		}

		var list []struct {
			ID        int    `json:"id"`
			Status    string `json:"status"`
			Ref       string `json:"ref"`
			CreatedAt string `json:"created_at"`
			Duration  int    `json:"duration"`
			WebURL    string `json:"web_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			writeJSON(w, []PipelineTrendItem{})
			return
		}

		out := make([]PipelineTrendItem, 0, len(list))
		for _, it := range list {
			out = append(out, PipelineTrendItem{
				ID:        it.ID,
				Status:    it.Status,
				Ref:       it.Ref,
				CreatedAt: it.CreatedAt,
				Duration:  it.Duration,
				WebURL:    it.WebURL,
			})
		}
		writeJSON(w, out)
	}
}

// ========== glab config auto-detection ==========

func readGLabConfig() (url, token string) {
	homes := []string{}
	if h, err := os.UserHomeDir(); err == nil {
		homes = append(homes, h)
	}
	if h := os.Getenv("HOME"); h != "" {
		homes = append(homes, h)
	}

	var raw []byte
	var foundPath string
	for _, home := range homes {
		for _, rel := range []string{
			filepath.Join(".config", "glab-cli", "config.yml"),
			filepath.Join(".config", "glab", "config.yml"),
			filepath.Join("AppData", "Roaming", "glab-cli", "config.yml"),
		} {
			p := filepath.Join(home, rel)
			if b, err := os.ReadFile(p); err == nil {
				raw = b
				foundPath = p
				break
			}
		}
		if raw != nil {
			break
		}
	}
	if raw == nil {
		return
	}
	log.Printf("gitlab: reading config from %s", foundPath)

	var cfg struct {
		Hosts map[string]struct {
			Token       string `yaml:"token"`
			APIProtocol string `yaml:"api_protocol"`
		} `yaml:"hosts"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return
	}
	for host, h := range cfg.Hosts {
		if h.Token == "" {
			continue
		}
		proto := h.APIProtocol
		if proto == "" {
			proto = "https"
		}
		return proto + "://" + host, h.Token
	}
	return
}
