package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpradmin/prompts-mcp/models"
)

// loadAllUncached performs the actual filesystem walk and YAML parse.
func (pl *PromptLoader) loadAllUncached() ([]models.Prompt, error) {
	var prompts []models.Prompt

	// Load from personal directory
	personalDir := filepath.Join(pl.baseDir, "personal")
	personalPrompts, err := pl.loadFromDirectory(personalDir, "project")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("error loading personal prompts: %w", err)
	}
	prompts = append(prompts, personalPrompts...)

	// Load from inherited directory
	inheritedDir := filepath.Join(pl.baseDir, "inherited")
	inheritedPrompts, err := pl.loadFromDirectory(inheritedDir, "global")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("error loading inherited prompts: %w", err)
	}
	prompts = append(prompts, inheritedPrompts...)

	return prompts, nil
}

// loadFromDirectory loads all prompts from a directory
func (pl *PromptLoader) loadFromDirectory(dir string, scope string) ([]models.Prompt, error) {
	var prompts []models.Prompt

	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return prompts, nil
	}

	// Walk through domain subdirectories
	domains := []string{"router-prompts", "conversation-prompts", "go-coding-prompts", "python-coding-prompts", "iac-prompts", "memory-prompts"}

	for _, domainDir := range domains {
		domainPath := filepath.Join(dir, domainDir)
		if _, err := os.Stat(domainPath); os.IsNotExist(err) {
			continue
		}

		// Read all YAML files in this domain directory
		files, err := os.ReadDir(domainPath)
		if err != nil {
			return nil, fmt.Errorf("error reading domain directory %s: %w", domainPath, err)
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			// Only process YAML files
			if !strings.HasSuffix(file.Name(), ".yaml") && !strings.HasSuffix(file.Name(), ".yml") {
				continue
			}

			filePath := filepath.Join(domainPath, file.Name())
			prompt, err := pl.parsePromptFile(filePath, scope)
			if err != nil {
				// Log error but continue processing other files
				fmt.Fprintf(os.Stderr, "Error parsing prompt file %s: %v\n", filePath, err)
				continue
			}

			prompts = append(prompts, *prompt)
		}
	}

	return prompts, nil
}
