package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v89/github"
)

// setupTestClient creates a test HTTP server and a GoGitHubClient pointed at it.
// Handlers should register on mux without any /api/v3 prefix — the prefix is stripped automatically.
func setupTestClient(t *testing.T, mux *http.ServeMux) (*GoGitHubClient, *httptest.Server) {
	t.Helper()

	// Wrap mux to strip /api/v3 prefix (added by WithEnterpriseURLs)
	handler := http.NewServeMux()
	handler.Handle("/api/v3/", http.StripPrefix("/api/v3", mux))

	srv := httptest.NewServer(handler)

	client, err := github.NewClient(
		github.WithHTTPClient(srv.Client()),
		github.WithEnterpriseURLs(srv.URL+"/api/v3/", srv.URL+"/api/v3/"),
	)
	if err != nil {
		t.Fatal(err)
	}

	return &GoGitHubClient{client: client, owner: "test-owner", repo: "test-repo"}, srv
}

// --- TagExists ---

func TestTagExists_Found(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/tags/v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Reference{
			Object: &github.GitObject{SHA: github.Ptr("abc123")},
		})
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	exists, sha, err := client.TagExists(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("expected tag to exist")
	}
	if sha != "abc123" {
		t.Fatalf("expected sha abc123, got %s", sha)
	}
}

func TestTagExists_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/tags/v9.9.9", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	exists, sha, err := client.TagExists(context.Background(), "v9.9.9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Fatal("expected tag to not exist")
	}
	if sha != "" {
		t.Fatalf("expected empty sha, got %s", sha)
	}
}

// --- CreateTag ---

func TestCreateTag_Success(t *testing.T) {
	mux := http.NewServeMux()
	var receivedRef string
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		receivedRef = body.Ref
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(github.Reference{
			Ref:    github.Ptr(body.Ref),
			Object: &github.GitObject{SHA: github.Ptr(body.SHA)},
		})
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	err := client.CreateTag(context.Background(), "tenant/dev/pkg/artifact/1.0.0", "commitsha123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedRef != "refs/tags/tenant/dev/pkg/artifact/1.0.0" {
		t.Fatalf("unexpected ref: %s", receivedRef)
	}
}

// --- ListOwners ---

func TestListOwners_SinglePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.User{Login: github.Ptr("myuser"), Type: github.Ptr("User")})
	})
	mux.HandleFunc("GET /user/orgs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]*github.Organization{
			{Login: github.Ptr("org1")},
			{Login: github.Ptr("org2")},
		})
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	owners, err := client.ListOwners(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(owners) != 3 {
		t.Fatalf("expected 3 owners, got %d", len(owners))
	}
	if owners[0].Login != "myuser" || owners[0].Type != "User" {
		t.Fatalf("first owner should be user, got %+v", owners[0])
	}
	if owners[1].Login != "org1" || owners[1].Type != "Organization" {
		t.Fatalf("second owner should be org1, got %+v", owners[1])
	}
}

func TestListOwners_Paginated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.User{Login: github.Ptr("myuser"), Type: github.Ptr("User")})
	})
	mux.HandleFunc("GET /user/orgs", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			// Page 1: return one org + Link header pointing to page 2
			w.Header().Set("Link", fmt.Sprintf(`<%s/user/orgs?page=2&per_page=100>; rel="next"`, server(r)))
			json.NewEncoder(w).Encode([]*github.Organization{
				{Login: github.Ptr("org-page1")},
			})
		} else {
			// Page 2: return one org, no next link
			json.NewEncoder(w).Encode([]*github.Organization{
				{Login: github.Ptr("org-page2")},
			})
		}
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	owners, err := client.ListOwners(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(owners) != 3 {
		t.Fatalf("expected 3 owners (user + 2 orgs across pages), got %d", len(owners))
	}
	if owners[1].Login != "org-page1" {
		t.Fatalf("expected org-page1, got %s", owners[1].Login)
	}
	if owners[2].Login != "org-page2" {
		t.Fatalf("expected org-page2, got %s", owners[2].Login)
	}
}

// --- ListRepos ---

func TestListRepos_User(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user/repos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]*github.Repository{
			{Name: github.Ptr("repo1"), FullName: github.Ptr("myuser/repo1"), Private: github.Ptr(false)},
			{Name: github.Ptr("repo2"), FullName: github.Ptr("myuser/repo2"), Private: github.Ptr(true)},
		})
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	repos, err := client.ListRepos(context.Background(), "myuser", "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	if repos[0].Name != "repo1" || repos[0].Private != false {
		t.Fatalf("unexpected repo[0]: %+v", repos[0])
	}
	if repos[1].Name != "repo2" || repos[1].Private != true {
		t.Fatalf("unexpected repo[1]: %+v", repos[1])
	}
}

func TestListRepos_Organization(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orgs/my-org/repos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]*github.Repository{
			{Name: github.Ptr("org-repo"), FullName: github.Ptr("my-org/org-repo"), Private: github.Ptr(false)},
		})
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	repos, err := client.ListRepos(context.Background(), "my-org", "Organization")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Name != "org-repo" {
		t.Fatalf("expected org-repo, got %s", repos[0].Name)
	}
}

func TestListRepos_Paginated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user/repos", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/user/repos?page=2&per_page=100>; rel="next"`, server(r)))
			json.NewEncoder(w).Encode([]*github.Repository{
				{Name: github.Ptr("repo-p1"), FullName: github.Ptr("u/repo-p1"), Private: github.Ptr(false)},
			})
		} else {
			json.NewEncoder(w).Encode([]*github.Repository{
				{Name: github.Ptr("repo-p2"), FullName: github.Ptr("u/repo-p2"), Private: github.Ptr(true)},
			})
		}
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	repos, err := client.ListRepos(context.Background(), "u", "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos across pages, got %d", len(repos))
	}
	if repos[0].Name != "repo-p1" {
		t.Fatalf("expected repo-p1, got %s", repos[0].Name)
	}
	if repos[1].Name != "repo-p2" {
		t.Fatalf("expected repo-p2, got %s", repos[1].Name)
	}
}

// --- ReadTree ---

func TestReadTree_FiltersByTreePath(t *testing.T) {
	mux := http.NewServeMux()

	fileContent := "hello world"
	encodedContent := base64.StdEncoding.EncodeToString([]byte(fileContent))

	mux.HandleFunc("GET /repos/test-owner/test-repo/git/trees/commitsha123", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("recursive") != "1" {
			t.Error("expected recursive=1")
		}
		json.NewEncoder(w).Encode(github.Tree{
			Entries: []*github.TreeEntry{
				{Path: github.Ptr("packages/pkg/artifact/src/main.groovy"), Type: github.Ptr("blob"), SHA: github.Ptr("blob1")},
				{Path: github.Ptr("packages/pkg/artifact/META-INF/MANIFEST.MF"), Type: github.Ptr("blob"), SHA: github.Ptr("blob2")},
				{Path: github.Ptr("packages/other/file.txt"), Type: github.Ptr("blob"), SHA: github.Ptr("blob3")},         // different path
				{Path: github.Ptr("packages/pkg/artifact/subdir"), Type: github.Ptr("tree"), SHA: github.Ptr("treeSHA")}, // tree entry, skip
			},
		})
	})
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/blobs/blob1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Blob{Content: github.Ptr(encodedContent), Encoding: github.Ptr("base64")})
	})
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/blobs/blob2", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Blob{Content: github.Ptr("manifest content"), Encoding: github.Ptr("utf-8")})
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	files, err := client.ReadTree(context.Background(), "commitsha123", "packages/pkg/artifact")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), keys(files))
	}
	if string(files["src/main.groovy"]) != fileContent {
		t.Fatalf("unexpected content for main.groovy: %q", string(files["src/main.groovy"]))
	}
	if string(files["META-INF/MANIFEST.MF"]) != "manifest content" {
		t.Fatalf("unexpected content for MANIFEST.MF: %q", string(files["META-INF/MANIFEST.MF"]))
	}
}

// --- Commit (tree-unchanged idempotency) ---

func TestCommit_TreeUnchanged_SkipsCommit(t *testing.T) {
	treeSHA := "existing-tree-sha"
	parentSHA := "parent-commit-sha"
	commitCreated := false

	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/heads/tenant/dev", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Reference{
			Object: &github.GitObject{SHA: github.Ptr(parentSHA)},
		})
	})
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/commits/{sha}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Commit{
			Tree: &github.Tree{SHA: github.Ptr(treeSHA)},
		})
	})
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Blob{SHA: github.Ptr("newblobsha")})
	})
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
		// Return same tree SHA as parent → tree unchanged
		json.NewEncoder(w).Encode(github.Tree{SHA: github.Ptr(treeSHA)})
	})
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
		commitCreated = true
		w.WriteHeader(http.StatusCreated)
	})

	client, server := setupTestClient(t, mux)
	defer server.Close()

	sha, err := client.Commit(context.Background(), "tenant/dev", "packages/pkg/art",
		FileMap{"file.txt": []byte("content")}, CommitMeta{Message: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sha != parentSHA {
		t.Fatalf("expected parent SHA returned when tree unchanged, got %s", sha)
	}
	if commitCreated {
		t.Fatal("commit should not have been created when tree is unchanged")
	}
}

func TestCommit_BranchAutoCreate(t *testing.T) {
	newCommitSHA := "new-commit-sha"
	orphanCommitSHA := "orphan-init-sha"
	branchCreated := false
	branchGetCount := 0

	mux := http.NewServeMux()
	// Branch: first call 404 (ensureBranch), second call returns the orphan init commit
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/heads/tenant/new-tenant", func(w http.ResponseWriter, r *http.Request) {
		branchGetCount++
		if branchGetCount <= 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(github.Reference{
			Object: &github.GitObject{SHA: github.Ptr(orphanCommitSHA)},
		})
	})
	// ensureBranch: CreateCommit (orphan, empty tree, no parents)
	commitCallCount := 0
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
		commitCallCount++
		if commitCallCount == 1 {
			// orphan init commit
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(github.Commit{SHA: github.Ptr(orphanCommitSHA)})
			return
		}
		// real artifact commit
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(github.Commit{SHA: github.Ptr(newCommitSHA)})
	})
	// ensureBranch: CreateRef
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/refs", func(w http.ResponseWriter, r *http.Request) {
		branchCreated = true
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(github.Reference{})
	})
	// Commit step 2: GetCommit (parent is orphan init → empty tree)
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/commits/{sha}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Commit{
			Tree: &github.Tree{SHA: github.Ptr(emptyTreeSHA)},
		})
	})
	// Commit step 3: CreateBlob
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Blob{SHA: github.Ptr("blobsha")})
	})
	// Commit step 4: CreateTree (different from empty tree → commit proceeds)
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Tree{SHA: github.Ptr("new-tree-sha")})
	})
	// Commit step 7: UpdateRef
	mux.HandleFunc("PATCH /repos/test-owner/test-repo/git/refs/heads/tenant/new-tenant", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Reference{})
	})

	client, srv := setupTestClient(t, mux)
	defer srv.Close()

	sha, err := client.Commit(context.Background(), "tenant/new-tenant", "pkg/art",
		FileMap{"file.txt": []byte("content")}, CommitMeta{Message: "sync"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sha != newCommitSHA {
		t.Fatalf("expected %s, got %s", newCommitSHA, sha)
	}
	if !branchCreated {
		t.Fatal("expected orphan branch to be auto-created")
	}
}

// --- NewGitClient factory ---

func TestNewGitClient_UnsupportedProvider(t *testing.T) {
	_, err := NewGitClient(context.Background(), "gitlab", "dest", "owner", "repo", nil)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if err.Error() != `unsupported git provider: "gitlab"` {
		t.Fatalf("unexpected error message: %s", err)
	}
}

func TestCommit_RetryOnRefConflict(t *testing.T) {
	newCommitSHA := "final-commit-sha"
	updateRefCalls := 0

	mux := http.NewServeMux()
	// Branch exists
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/ref/heads/tenant/dev", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Reference{
			Object: &github.GitObject{SHA: github.Ptr("parent-sha")},
		})
	})
	// Get parent commit
	mux.HandleFunc("GET /repos/test-owner/test-repo/git/commits/{sha}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Commit{
			Tree: &github.Tree{SHA: github.Ptr("base-tree-sha")},
		})
	})
	// Create blob
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Blob{SHA: github.Ptr("blobsha")})
	})
	// Create tree (different from base)
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/trees", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(github.Tree{SHA: github.Ptr("new-tree-sha")})
	})
	// Create commit
	mux.HandleFunc("POST /repos/test-owner/test-repo/git/commits", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(github.Commit{SHA: github.Ptr(newCommitSHA)})
	})
	// UpdateRef: first call → 422 (conflict), second call → success
	mux.HandleFunc("PATCH /repos/test-owner/test-repo/git/refs/heads/tenant/dev", func(w http.ResponseWriter, r *http.Request) {
		updateRefCalls++
		if updateRefCalls == 1 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"Update is not a fast forward"}`)
			return
		}
		json.NewEncoder(w).Encode(github.Reference{})
	})

	client, srv := setupTestClient(t, mux)
	defer srv.Close()

	sha, err := client.Commit(context.Background(), "tenant/dev", "pkg/art",
		FileMap{"file.txt": []byte("content")}, CommitMeta{Message: "sync"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sha != newCommitSHA {
		t.Fatalf("expected %s, got %s", newCommitSHA, sha)
	}
	if updateRefCalls != 2 {
		t.Fatalf("expected 2 UpdateRef calls (1 conflict + 1 success), got %d", updateRefCalls)
	}
}

// --- normalizeGitURL ---

func TestNormalizeGitURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"https already", "https://github.wdf.sap.corp", "https://github.wdf.sap.corp"},
		{"https with trailing slash", "https://github.wdf.sap.corp/", "https://github.wdf.sap.corp"},
		{"http upgraded to https", "http://github.wdf.sap.corp", "https://github.wdf.sap.corp"},
		{"http with path", "http://github.wdf.sap.corp/api/v3/", "https://github.wdf.sap.corp/api/v3"},
		{"http with port 443", "http://github.wdf.sap.corp:443", "https://github.wdf.sap.corp:443"},
		{"https github.com", "https://api.github.com", "https://api.github.com"},
		{"http github.com upgraded", "http://api.github.com", "https://api.github.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeGitURL(tt.input)
			if got != tt.want {
				t.Errorf("normalizeGitURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- helpers ---

// server extracts the test server base URL from a request (for building Link headers).
func server(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

func keys(m FileMap) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
