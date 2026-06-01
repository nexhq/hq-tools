package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

const OrgName = "nexhq"

type Repo struct {
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
}

type Registry struct {
	Schema       string                   `json:"$schema"`
	LastUpdated  string                   `json:"lastUpdated"`
	Organization string                   `json:"organization"`
	Tools        []map[string]interface{} `json:"tools"`
}

func main() {
	fmt.Printf("Fetching public repositories for '%s'...\n", OrgName)
	token := os.Getenv("GITHUB_TOKEN")

	repos, err := fetchPublicRepos(token)
	if err != nil {
		fmt.Printf("Error fetching repos: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found %d public repositories.\n", len(repos))

	// Setup concurrency
	var wg sync.WaitGroup
	var mu sync.Mutex
	var tools []map[string]interface{}

	fmt.Println("Scanning repositories concurrently for nex.json manifests...")
	for _, repo := range repos {
		if repo.Name == "hq-tools" {
			continue // Skip self
		}
		if repo.DefaultBranch == "" {
			repo.DefaultBranch = "main"
		}

		wg.Add(1)
		// Launch a Goroutine for each repository
		go func(r Repo) {
			defer wg.Done()
			manifest := fetchNexManifest(r.Name, r.DefaultBranch, token)
			if manifest != nil {
				// Lock mutex to prevent race conditions when appending
				mu.Lock()
				tools = append(tools, manifest)
				mu.Unlock()

				name, _ := manifest["name"].(string)
				fmt.Printf("  [+] Found valid tool manifest: %s\n", name)
			}
		}(repo)
	}

	// Wait for all Goroutines to finish
	wg.Wait()

	// Sort the tools strictly by name for consistent payload generation
	sort.Slice(tools, func(i, j int) bool {
		nameI, _ := tools[i]["name"].(string)
		nameJ, _ := tools[j]["name"].(string)
		return nameI < nameJ
	})

	// Construct final registry
	registry := Registry{
		Schema:       "https://raw.githubusercontent.com/nexhq/hq-tools/main/schema/tools.schema.json",
		LastUpdated:  time.Now().UTC().Format(time.RFC3339) + "Z", // mimic ISO 8601 UTC
		Organization: OrgName,
		Tools:        tools,
	}

	// Prevent null array in JSON (default [] instead of null)
	if registry.Tools == nil {
		registry.Tools = []map[string]interface{}{}
	}

	// Write output
	outFile := "tools.json"
	writeRegistry(outFile, registry)

	fmt.Printf("\nSuccess! Registry built with %d tools.\n", len(registry.Tools))
	fmt.Printf("Output saved to %s\n", outFile)
}

// fetchPublicRepos pages through the GitHub API
func fetchPublicRepos(token string) ([]Repo, error) {
	var allRepos []Repo
	page := 1

	for {
		url := fmt.Sprintf("https://api.github.com/orgs/%s/repos?type=public&per_page=100&page=%d", OrgName, page)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("failed: %d - %s", resp.StatusCode, string(body))
		}

		var repos []Repo
		if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		if len(repos) == 0 {
			break
		}
		allRepos = append(allRepos, repos...)
		page++
	}
	return allRepos, nil
}

// fetchNexManifest pulls the JSON layout
func fetchNexManifest(repoName, defaultBranch, token string) map[string]interface{} {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/nex.json", OrgName, repoName, defaultBranch)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var manifest map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&manifest); err == nil {
			return manifest
		}
	}
	return nil
}

// writeRegistry exports to file
func writeRegistry(filepath string, registry Registry) {
	file, err := os.Create(filepath)
	if err != nil {
		fmt.Printf("Failed to create file: %v\n", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(registry); err != nil {
		fmt.Printf("Failed to write json: %v\n", err)
	}
}
