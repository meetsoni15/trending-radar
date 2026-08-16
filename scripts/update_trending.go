// Command update_trending fetches trending/new GitHub repos per category and
// rewrites README.md between the TRENDING:START / TRENDING:END markers.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	startMarker      = "<!-- TRENDING:START -->"
	endMarker        = "<!-- TRENDING:END -->"
	perCategoryLimit = 8
	lookbackDays     = 7
	minStarsOverall  = 50
	apiRoot          = "https://api.github.com/search/repositories"
)

// Each category is a list of GitHub topic slugs; results across topics are merged & deduped.
// RequireLanguage, if set, drops results whose primary language doesn't match
// (topics like "go" get attached to repos that merely embed a Go component,
// e.g. an Electron app with a Go backend, so topic search alone isn't enough).
var categories = []struct {
	Label           string
	Topics          []string
	RequireLanguage string
}{
	{"🤖 AI Agents & LLM Tools", []string{"ai-agents", "llm-agents", "agentic-ai"}, ""},
	{"🛡️ Security, Hacking & Pentesting", []string{"pentesting", "security-tools", "ai-security", "hacking", "cybersecurity", "ctf"}, ""},
	{"🐹 Go Projects", []string{"golang", "go"}, "Go"},
	{"🛠️ Developer Tools & CLI", []string{"developer-tools", "cli"}, ""},
	{"⚙️ DevOps & Infrastructure", []string{"devops", "infrastructure-as-code"}, ""},
}

type repo struct {
	FullName        string `json:"full_name"`
	HTMLURL         string `json:"html_url"`
	StargazersCount int    `json:"stargazers_count"`
	Language        string `json:"language"`
	Description     string `json:"description"`
}

type searchResponse struct {
	Items []repo `json:"items"`
}

func readmePath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "README.md")
}

func ghGet(rawURL string) (searchResponse, error) {
	var out searchResponse
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return out, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "weekly-trending-radar")
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return out, err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return out, readErr
		}

		if resp.StatusCode == http.StatusForbidden && attempt < 2 {
			time.Sleep(10 * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return out, fmt.Errorf("GET %s: HTTP %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return out, err
		}
		return out, nil
	}
	return out, fmt.Errorf("GET %s: rate limited after retries", rawURL)
}

func searchRepos(query string) ([]repo, error) {
	u := fmt.Sprintf("%s?q=%s&sort=stars&order=desc&per_page=15", apiRoot, url.QueryEscape(query))
	resp, err := ghGet(u)
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func sinceDate() string {
	return time.Now().AddDate(0, 0, -lookbackDays).Format("2006-01-02")
}

func fetchCategory(topics []string, requireLanguage string) []repo {
	seen := map[string]repo{}
	for _, topic := range topics {
		query := fmt.Sprintf("topic:%s created:>%s", topic, sinceDate())
		repos, err := searchRepos(query)
		if err != nil {
			fmt.Printf("  warn: query failed for topic=%s: %v\n", topic, err)
		}
		for _, r := range repos {
			if requireLanguage != "" && r.Language != requireLanguage {
				continue
			}
			seen[r.FullName] = r
		}
		time.Sleep(2 * time.Second) // stay under search rate limits
	}

	ranked := make([]repo, 0, len(seen))
	for _, r := range seen {
		ranked = append(ranked, r)
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].StargazersCount > ranked[j].StargazersCount })
	if len(ranked) > perCategoryLimit {
		ranked = ranked[:perCategoryLimit]
	}
	return ranked
}

func fetchOverallNew() []repo {
	query := fmt.Sprintf("created:>%s stars:>%d", sinceDate(), minStarsOverall)
	repos, err := searchRepos(query)
	if err != nil {
		fmt.Printf("  warn: overall query failed: %v\n", err)
		return nil
	}
	if len(repos) > 10 {
		repos = repos[:10]
	}
	return repos
}

func renderTable(repos []repo) string {
	if len(repos) == 0 {
		return "_No qualifying repos found this week._\n"
	}
	var b strings.Builder
	b.WriteString("| Repo | ⭐ Stars | Language | Description |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, r := range repos {
		lang := r.Language
		if lang == "" {
			lang = "—"
		}
		desc := strings.ReplaceAll(strings.TrimSpace(r.Description), "|", "/")
		if runes := []rune(desc); len(runes) > 100 {
			desc = string(runes[:97]) + "..."
		}
		fmt.Fprintf(&b, "| [%s](%s) | %s | %s | %s |\n", r.FullName, r.HTMLURL, formatStars(r.StargazersCount), lang, desc)
	}
	return b.String()
}

func formatStars(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

func buildSection() string {
	today := time.Now().Format("2006-01-02")
	var b strings.Builder
	fmt.Fprintf(&b, "_Last updated: **%s** (UTC) · repos created in the last %d days_\n\n", today, lookbackDays)

	b.WriteString("## 🔥 New This Week (overall)\n\n")
	b.WriteString(renderTable(fetchOverallNew()))

	for _, c := range categories {
		fmt.Printf("Fetching category: %s\n", c.Label)
		fmt.Fprintf(&b, "\n## %s\n\n", c.Label)
		b.WriteString(renderTable(fetchCategory(c.Topics, c.RequireLanguage)))
	}
	return b.String()
}

func updateReadme(section string) error {
	path := readmePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)

	startIdx := strings.Index(text, startMarker)
	endIdx := strings.Index(text, endMarker)
	if startIdx == -1 || endIdx == -1 {
		return fmt.Errorf("README markers not found")
	}

	before := text[:startIdx]
	after := text[endIdx+len(endMarker):]
	newText := before + startMarker + "\n" + section + "\n" + endMarker + after
	return os.WriteFile(path, []byte(newText), 0o644)
}

func main() {
	section := buildSection()
	if err := updateReadme(section); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("README.md updated.")
}
