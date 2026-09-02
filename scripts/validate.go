package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Allowed Categories for Tracks
var validCategories = map[string]bool{
	"lang":  true,
	"os":    true,
	"cloud": true,
	"db":    true,
	"tool":  true,
}

// AST Node
type Node struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "file" or "directory"
	Content  string `json:"content,omitempty"`
	Children []Node `json:"children,omitempty"`
}

// Blueprint Structure
type Blueprint struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Root        Node   `json:"root"`
}

func main() {
	fmt.Println("=======================================================")
	fmt.Println("  🔍 Trak Registry - Blueprint AST Schema Validator")
	fmt.Println("=======================================================")
	fmt.Println()

	actor := strings.TrimSpace(os.Getenv("GITHUB_ACTOR"))
	repoOwner := strings.TrimSpace(os.Getenv("REPO_OWNER"))
	eventName := strings.TrimSpace(os.Getenv("GITHUB_EVENT_NAME"))

	// --------------------------------------------------------------------
	// STEP 1 & 2: PR Author & Git Diff Target Namespace Verification
	// --------------------------------------------------------------------
	if eventName == "pull_request" && actor != "" {
		isRepoOwner := repoOwner != "" && strings.EqualFold(actor, repoOwner)

		fmt.Printf("📋 PR Event Detected | Author: @%s | Target Repo Owner: @%s\n", actor, repoOwner)

		changedFiles := getChangedFiles()
		if len(changedFiles) > 0 {
			fmt.Printf("🔍 Detected %d changed file(s) in this PR:\n", len(changedFiles))
			for _, file := range changedFiles {
				fmt.Printf("   - %s\n", file)
			}
			fmt.Println()
		}

		if !isRepoOwner {
			// External contributor security checks on changed files
			for _, file := range changedFiles {
				normalizedFile := filepath.ToSlash(strings.TrimPrefix(file, "./"))

				// Rule A: Contributor cannot touch scripts/ or .github/ workflows
				if strings.HasPrefix(normalizedFile, "scripts/") || strings.HasPrefix(normalizedFile, ".github/") {
					fmt.Printf("❌ Security Violation: PR author '@%s' cannot modify CI workflows or scripts ('%s')\n", actor, normalizedFile)
					os.Exit(1)
				}

				// Rule B: Contributor cannot modify official templates/
				if strings.HasPrefix(normalizedFile, "templates/") {
					fmt.Printf("❌ Security Violation: PR author '@%s' cannot modify official templates/. Please submit under 'users/%s/<category>/<track>.json'\n", actor, actor)
					os.Exit(1)
				}

				// Rule C: Contributor MUST only modify files under users/<actor>/<category>/<slug>[@<version>][.json]
				if strings.HasPrefix(normalizedFile, "users/") {
					parts := strings.Split(normalizedFile, "/")
					if len(parts) != 4 {
						fmt.Printf("❌ Path Error: Community file '%s' must match 'users/%s/<category>/<track>.json'\n", normalizedFile, actor)
						os.Exit(1)
					}
					folderUser := parts[1]
					category := parts[2]
					fileName := parts[3]

					// if not json then issue 
					if !strings.HasSuffix(fileName, ".json") {
						fmt.Printf("❌ Path Error: Community file '%s' must end in .json or contain @version tag\n", normalizedFile)
						os.Exit(1)
					}
						
					if !strings.EqualFold(folderUser, actor) {
						fmt.Printf("❌ Security Violation: PR author '@%s' cannot modify namespace of another user 'users/%s/'\n", actor, folderUser)
						os.Exit(1)
					}
					if !validCategories[category] {
						fmt.Printf("❌ Invalid Category: '%s' is not allowed. Must be one of: lang, os, cloud, db, tool\n", category)
						os.Exit(1)
					}
				} else {
					// Touched root files or unknown paths
					fmt.Printf("❌ Security Violation: PR author '@%s' cannot modify root repository files ('%s')\n", actor, normalizedFile)
					os.Exit(1)
				}
			}
		}
	}

	// --------------------------------------------------------------------
	// STEP 3: Schema & AST Validation across all blueprints
	// --------------------------------------------------------------------
	errorsCount := 0
	filesCount := 0

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		normalizedPath := filepath.ToSlash(path)
		normalizedPath = strings.TrimPrefix(normalizedPath, "./")

		baseName := filepath.Base(normalizedPath)
		if !strings.HasSuffix(normalizedPath, ".json") && !strings.Contains(baseName, "@") {
			return nil
		}
		if normalizedPath == "registry.json" || normalizedPath == "package.json" {
			return nil
		}

		isOfficial := strings.HasPrefix(normalizedPath, "templates/")
		isUser := strings.HasPrefix(normalizedPath, "users/")

		if !isOfficial && !isUser {
			return nil
		}

		filesCount++
		fmt.Printf("🔍 [%s] Checking: %-45s ", formatType(isOfficial), normalizedPath)

		if err := validateBlueprintFile(normalizedPath, isOfficial); err != nil {
			fmt.Printf("❌ FAILED\n   Error: %v\n", err)
			errorsCount++
		} else {
			fmt.Println("✔ PASSED")
		}

		return nil
	})

	if err != nil {
		fmt.Printf("\n❌ Error walking registry directories: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("=======================================================")
	if errorsCount > 0 {
		fmt.Printf("  ❌ Validation FAILED with %d error(s) across %d files.\n", errorsCount, filesCount)
		fmt.Println("=======================================================")
		os.Exit(1)
	}

	fmt.Printf("  ✨ All %d Blueprints PASSED Validation Successfully! 🎉\n", filesCount)
	fmt.Println("=======================================================")
}

func getChangedFiles() []string {
	var changed []string

	// 1. Check CHANGED_FILES env var from GitHub Actions
	if envList := strings.TrimSpace(os.Getenv("CHANGED_FILES")); envList != "" {
		for _, f := range strings.Split(envList, "\n") {
			f = strings.TrimSpace(filepath.ToSlash(f))
			f = strings.TrimPrefix(f, "./")
			if f != "" {
				changed = append(changed, f)
			}
		}
		return changed
	}

	// 2. Try git diff commands
	baseRef := os.Getenv("GITHUB_BASE_REF")
	if baseRef == "" {
		baseRef = "main"
	}
	out, err := exec.Command("git", "diff", "--name-only", "origin/"+baseRef+"...HEAD").Output()
	if err == nil && len(out) > 0 {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.TrimSpace(filepath.ToSlash(line))
			f = strings.TrimPrefix(f, "./")
			if f != "" {
				changed = append(changed, f)
			}
		}
		return changed
	}

	// Fallback: git diff HEAD~1
	out, err = exec.Command("git", "diff", "--name-only", "HEAD~1").Output()
	if err == nil && len(out) > 0 {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.TrimSpace(filepath.ToSlash(line))
			f = strings.TrimPrefix(f, "./")
			if f != "" {
				changed = append(changed, f)
			}
		}
		return changed
	}

	return changed
}

func formatType(isOfficial bool) string {
	if isOfficial {
		return "OFFICIAL"
	}
	return "COMMUNITY"
}

func validateBlueprintFile(filePath string, isOfficial bool) error {
	// 1. Path structure verification
	parts := strings.Split(filePath, "/")
	if isOfficial {
		// Expect: templates/<category>/<slug>.json (3 parts)
		if len(parts) != 3 {
			return fmt.Errorf("official template path must be 'templates/<category>/<slug>.json', got '%s'", filePath)
		}
		category := parts[1]
		if !validCategories[category] {
			return fmt.Errorf("invalid category '%s'. Must be one of: lang, os, cloud, db, tool", category)
		}
	} else {
		// Expect: users/<username>/<category>/<slug>.json (4 parts)
		if len(parts) != 4 {
			return fmt.Errorf("community template path must be 'users/<username>/<category>/<slug>.json', got '%s'", filePath)
		}
		category := parts[2]
		if !validCategories[category] {
			return fmt.Errorf("invalid category '%s'. Must be one of: lang, os, cloud, db, tool", category)
		}
	}

	// 2. Read and parse JSON
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}

	// Check max size (5MB limit per blueprint)
	if len(data) > 5*1024*1024 {
		return fmt.Errorf("file size (%d bytes) exceeds maximum 5MB limit", len(data))
	}

	var bp Blueprint
	if err := json.Unmarshal(data, &bp); err != nil {
		return fmt.Errorf("invalid JSON syntax: %w", err)
	}

	// 3. Metadata Validation
	if strings.TrimSpace(bp.ID) == "" {
		return fmt.Errorf("missing mandatory field 'id'")
	}
	if strings.TrimSpace(bp.Name) == "" {
		return fmt.Errorf("missing mandatory field 'name'")
	}
	if strings.TrimSpace(bp.Version) == "" {
		return fmt.Errorf("missing mandatory field 'version'")
	}

	// 4. Root AST validation
	if bp.Root.Type != "directory" {
		return fmt.Errorf("root node type must be 'directory', got '%s'", bp.Root.Type)
	}
	if strings.TrimSpace(bp.Root.Name) == "" {
		return fmt.Errorf("root node must have a non-empty name")
	}

	// 5. Recursive Node Validation
	return validateNode(&bp.Root, "/")
}

func validateNode(n *Node, parentPath string) error {
	if strings.TrimSpace(n.Name) == "" {
		return fmt.Errorf("node at '%s' has an empty name", parentPath)
	}

	// Prevent path traversal and illegal characters
	if strings.ContainsAny(n.Name, "/\\:\x00") || n.Name == ".." || n.Name == "." {
		return fmt.Errorf("illegal node name '%s' at '%s'", n.Name, parentPath)
	}

	currentPath := parentPath + n.Name

	switch n.Type {
	case "file":
		if len(n.Children) > 0 {
			return fmt.Errorf("file node '%s' cannot have children", currentPath)
		}
		// Disallow dangerous executable binaries inside templates
		lowerName := strings.ToLower(n.Name)
		if strings.HasSuffix(lowerName, ".exe") || strings.HasSuffix(lowerName, ".dll") || strings.HasSuffix(lowerName, ".so") || strings.HasSuffix(lowerName, ".dylib") {
			return fmt.Errorf("forbidden binary file '%s' detected inside template", currentPath)
		}
	case "directory":
		for i := range n.Children {
			if err := validateNode(&n.Children[i], currentPath+"/"); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown node type '%s' at '%s' (must be 'file' or 'directory')", n.Type, currentPath)
	}

	return nil
}
