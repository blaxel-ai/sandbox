package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/sirupsen/logrus"
)

// GitManager handles git operations
type GitManager struct {
	root       string
	workingDir string
}

// NewGitManager creates a new git manager
func NewGitManager(root, workingDir string) *GitManager {
	return &GitManager{
		root:       root,
		workingDir: workingDir,
	}
}

// FileStatus represents the status of a file in the repository
type FileStatus struct {
	Name     string `json:"name"`
	Staging  string `json:"staging"`  // "modified", "added", "deleted", "renamed", "untracked"
	Worktree string `json:"worktree"` // "modified", "added", "deleted", "renamed", "untracked"
}

// RepositoryStatus represents the status of a git repository
type RepositoryStatus struct {
	CurrentBranch string       `json:"currentBranch"`
	FileStatus    []FileStatus `json:"fileStatus"`
	Ahead         int          `json:"ahead"`
	Behind        int          `json:"behind"`
}

// BranchInfo represents information about branches
type BranchInfo struct {
	Branches []string `json:"branches"`
}

// GetAbsolutePath resolves a path to an absolute path
// This follows the same logic as the filesystem handler
func (gm *GitManager) GetAbsolutePath(path string) (string, error) {
	var absPath string

	// If path is absolute (starts with /), use it directly
	if filepath.IsAbs(path) {
		absPath = path
	} else {
		// If path is relative, resolve it from the working directory
		absPath = filepath.Join(gm.workingDir, path)
	}

	// Clean the path to resolve . and .. references
	absPath = filepath.Clean(absPath)

	// For absolute paths outside the root, we don't restrict access
	// This allows accessing system directories when using absolute paths
	// For relative paths, we still ensure they're within bounds
	if !filepath.IsAbs(path) {
		// Verify the path is within the root to prevent path traversal for relative paths
		if relPath, err := filepath.Rel(gm.root, absPath); err != nil || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path is outside of the root directory")
		}
	}

	return absPath, nil
}

// Clone clones a git repository
func (gm *GitManager) Clone(url, path, branch, username, password string) error {
	absPath, err := gm.GetAbsolutePath(path)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Check if directory already exists
	if _, err := os.Stat(absPath); err == nil {
		return fmt.Errorf("directory already exists: %s", absPath)
	}

	// Create parent directories if they don't exist
	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directories: %w", err)
	}

	cloneOptions := &git.CloneOptions{
		URL:      url,
		Progress: nil, // We could add progress tracking here
	}

	// Add authentication if provided
	if username != "" || password != "" {
		cloneOptions.Auth = &http.BasicAuth{
			Username: username,
			Password: password,
		}
	}

	// Add branch if specified
	if branch != "" {
		cloneOptions.ReferenceName = plumbing.NewBranchReferenceName(branch)
		cloneOptions.SingleBranch = true
	}

	logrus.Debugf("Cloning repository from %s to %s", url, absPath)
	_, err = git.PlainClone(absPath, false, cloneOptions)
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	return nil
}

// openRepository opens a git repository at the given path
func (gm *GitManager) openRepository(repoPath string) (*git.Repository, error) {
	absPath, err := gm.GetAbsolutePath(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	repo, err := git.PlainOpen(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	return repo, nil
}

// Status returns the status of a git repository
func (gm *GitManager) Status(repoPath string) (*RepositoryStatus, error) {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return nil, err
	}

	// Get current branch
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	currentBranch := head.Name().Short()

	// Get working tree status
	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	// Convert status to our format
	fileStatuses := make([]FileStatus, 0)
	for path, fileStatus := range status {
		fs := FileStatus{
			Name:     path,
			Staging:  convertStatusCode(fileStatus.Staging),
			Worktree: convertStatusCode(fileStatus.Worktree),
		}
		fileStatuses = append(fileStatuses, fs)
	}

	// Calculate ahead/behind counts
	ahead := 0
	behind := 0

	// Try to get the remote tracking branch
	if head.Name().IsBranch() {
		remoteBranch := fmt.Sprintf("refs/remotes/origin/%s", currentBranch)
		remoteRef, err := repo.Reference(plumbing.ReferenceName(remoteBranch), true)
		if err == nil {
			// Get commits between local and remote
			localCommit, _ := repo.CommitObject(head.Hash())
			remoteCommit, _ := repo.CommitObject(remoteRef.Hash())

			if localCommit != nil && remoteCommit != nil {
				// Count commits ahead (local commits not in remote)
				commits, err := repo.Log(&git.LogOptions{From: head.Hash()})
				if err == nil {
					err = commits.ForEach(func(c *object.Commit) error {
						if c.Hash == remoteCommit.Hash {
							return fmt.Errorf("stop") // Stop iteration
						}
						ahead++
						return nil
					})
					if err != nil && err.Error() != "stop" {
						logrus.Warnf("Failed to count ahead commits: %v", err)
					}
				}

				// Count commits behind (remote commits not in local)
				commits, err = repo.Log(&git.LogOptions{From: remoteRef.Hash()})
				if err == nil {
					err = commits.ForEach(func(c *object.Commit) error {
						if c.Hash == localCommit.Hash {
							return fmt.Errorf("stop") // Stop iteration
						}
						behind++
						return nil
					})
					if err != nil && err.Error() != "stop" {
						logrus.Warnf("Failed to count behind commits: %v", err)
					}
				}
			}
		}
	}

	return &RepositoryStatus{
		CurrentBranch: currentBranch,
		FileStatus:    fileStatuses,
		Ahead:         ahead,
		Behind:        behind,
	}, nil
}

// convertStatusCode converts git status code to string
func convertStatusCode(code git.StatusCode) string {
	switch code {
	case git.Unmodified:
		return "unmodified"
	case git.Untracked:
		return "untracked"
	case git.Modified:
		return "modified"
	case git.Added:
		return "added"
	case git.Deleted:
		return "deleted"
	case git.Renamed:
		return "renamed"
	case git.Copied:
		return "copied"
	case git.UpdatedButUnmerged:
		return "updated-but-unmerged"
	default:
		return "unknown"
	}
}

// Branches lists all branches in a repository
func (gm *GitManager) Branches(repoPath string) (*BranchInfo, error) {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return nil, err
	}

	branches := make([]string, 0)

	// Get all references
	refs, err := repo.Branches()
	if err != nil {
		return nil, fmt.Errorf("failed to get branches: %w", err)
	}

	err = refs.ForEach(func(ref *plumbing.Reference) error {
		branches = append(branches, ref.Name().Short())
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate branches: %w", err)
	}

	return &BranchInfo{
		Branches: branches,
	}, nil
}

// CreateBranch creates a new branch
func (gm *GitManager) CreateBranch(repoPath, branchName string) error {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return err
	}

	// Get HEAD reference
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}

	// Create branch reference
	branchRef := plumbing.NewBranchReferenceName(branchName)
	ref := plumbing.NewHashReference(branchRef, head.Hash())

	err = repo.Storer.SetReference(ref)
	if err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	return nil
}

// CheckoutBranch switches to a branch
func (gm *GitManager) CheckoutBranch(repoPath, branchName string) error {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Checkout the branch
	branchRef := plumbing.NewBranchReferenceName(branchName)
	err = worktree.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
	})
	if err != nil {
		return fmt.Errorf("failed to checkout branch: %w", err)
	}

	return nil
}

// DeleteBranch deletes a branch
func (gm *GitManager) DeleteBranch(repoPath, branchName string) error {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return err
	}

	// Check if we're trying to delete the current branch
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}

	if head.Name().Short() == branchName {
		return fmt.Errorf("cannot delete the current branch")
	}

	// Delete the branch
	branchRef := plumbing.NewBranchReferenceName(branchName)
	err = repo.Storer.RemoveReference(branchRef)
	if err != nil {
		return fmt.Errorf("failed to delete branch: %w", err)
	}

	return nil
}

// Add stages files for commit
func (gm *GitManager) Add(repoPath string, files []string) error {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Add each file
	for _, file := range files {
		// Handle "." for all files
		if file == "." {
			err = worktree.AddWithOptions(&git.AddOptions{
				All: true,
			})
			if err != nil {
				return fmt.Errorf("failed to add all files: %w", err)
			}
			return nil
		}

		// Add individual file
		_, err = worktree.Add(file)
		if err != nil {
			return fmt.Errorf("failed to add file %s: %w", file, err)
		}
	}

	return nil
}

// Commit creates a commit with staged changes
func (gm *GitManager) Commit(repoPath, message, author, email string) (string, error) {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return "", err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	// Create commit
	commitHash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  author,
			Email: email,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	return commitHash.String(), nil
}

// Push pushes commits to remote
func (gm *GitManager) Push(repoPath, username, password string) error {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return err
	}

	pushOptions := &git.PushOptions{}

	// Add authentication if provided
	if username != "" || password != "" {
		pushOptions.Auth = &http.BasicAuth{
			Username: username,
			Password: password,
		}
	}

	err = repo.Push(pushOptions)
	if err != nil {
		// git.NoErrAlreadyUpToDate is not an error
		if err == git.NoErrAlreadyUpToDate {
			return nil
		}
		return fmt.Errorf("failed to push: %w", err)
	}

	return nil
}

// Pull pulls commits from remote
func (gm *GitManager) Pull(repoPath, username, password string) error {
	repo, err := gm.openRepository(repoPath)
	if err != nil {
		return err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	pullOptions := &git.PullOptions{
		RemoteName: "origin",
	}

	// Add authentication if provided
	if username != "" || password != "" {
		pullOptions.Auth = &http.BasicAuth{
			Username: username,
			Password: password,
		}
	}

	err = worktree.Pull(pullOptions)
	if err != nil {
		// git.NoErrAlreadyUpToDate is not an error
		if err == git.NoErrAlreadyUpToDate {
			return nil
		}
		return fmt.Errorf("failed to pull: %w", err)
	}

	return nil
}

// ValidateGitURL validates a git URL to prevent injection attacks
func ValidateGitURL(url string) error {
	// Check for basic git URL patterns
	if url == "" {
		return fmt.Errorf("URL cannot be empty")
	}

	// Allow HTTP(S) and SSH URLs
	if !strings.HasPrefix(url, "http://") &&
		!strings.HasPrefix(url, "https://") &&
		!strings.HasPrefix(url, "git@") &&
		!strings.HasPrefix(url, "ssh://") {
		return fmt.Errorf("invalid git URL format")
	}

	// Prevent command injection through URL
	if strings.Contains(url, ";") || strings.Contains(url, "|") ||
		strings.Contains(url, "&") || strings.Contains(url, "`") ||
		strings.Contains(url, "$") || strings.Contains(url, "\n") {
		return fmt.Errorf("URL contains invalid characters")
	}

	return nil
}

// ValidatePath validates a path to prevent directory traversal
func ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	// Prevent directory traversal
	if strings.Contains(path, "..") {
		return fmt.Errorf("path contains directory traversal")
	}

	return nil
}
