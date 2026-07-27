package tools

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MemoryPattern represents an extracted pattern from a memory file
type MemoryPattern struct {
	Domain        string
	PatternName   string
	PatternText   string
	PatternType   string // 'success', 'failure', 'gotcha', 'discovery'
	SourceFile    string
	SourceSection string // 'Accomplished', 'Discoveries', 'Next Steps'
	ExtractedAt   time.Time
}

// MemoryExtractor reads memory files and extracts patterns
type MemoryExtractor struct {
	memoryDir string
}

// NewMemoryExtractor creates a new memory extractor
func NewMemoryExtractor(memoryDir string) *MemoryExtractor {
	return &MemoryExtractor{
		memoryDir: memoryDir,
	}
}

// ExtractFromFile reads a single memory file and extracts patterns
func (me *MemoryExtractor) ExtractFromFile(filePath string) ([]MemoryPattern, error) {
	var patterns []MemoryPattern

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	text := string(content)
	relPath, _ := filepath.Rel(me.memoryDir, filePath)

	// Extract domain if present in frontmatter
	domain := me.extractDomain(text)

	// Extract patterns from key sections
	patterns = append(patterns, me.extractSection(text, "## Accomplished", "success", relPath, domain)...)
	patterns = append(patterns, me.extractSection(text, "## Discoveries", "discovery", relPath, domain)...)
	patterns = append(patterns, me.extractSection(text, "## Next Steps", "pending", relPath, domain)...)

	// Mark gotchas (⚠️ items)
	patterns = append(patterns, me.extractGotchas(text, relPath, domain)...)

	return patterns, nil
}

// ExtractAll walks the memory directory and extracts all patterns
func (me *MemoryExtractor) ExtractAll() ([]MemoryPattern, error) {
	var allPatterns []MemoryPattern

	err := filepath.Walk(me.memoryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process markdown files
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		patterns, err := me.ExtractFromFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error extracting from %s: %v\n", path, err)
			return nil // Continue with other files
		}

		allPatterns = append(allPatterns, patterns...)
		return nil
	})

	return allPatterns, err
}

// extractSection extracts patterns from a specific section (## Accomplished, ## Discoveries, etc.)
func (me *MemoryExtractor) extractSection(content, sectionHeader, patternType, sourceFile, domain string) []MemoryPattern {
	var patterns []MemoryPattern

	// Find section in content
	sectionStart := strings.Index(content, sectionHeader)
	if sectionStart == -1 {
		return patterns
	}

	// Find next section marker or end of content
	contentAfter := content[sectionStart+len(sectionHeader):]
	nextSectionIdx := strings.Index(contentAfter, "## ")
	sectionEnd := len(contentAfter)
	if nextSectionIdx != -1 {
		sectionEnd = nextSectionIdx
	}

	sectionContent := contentAfter[:sectionEnd]

	// Extract bullet points (lines starting with - or ✅ or 🔲)
	lines := strings.Split(sectionContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "✅") &&
			!strings.HasPrefix(line, "🔲") && !strings.HasPrefix(line, "📋") {
			continue
		}

		// Clean the line
		pattern := me.cleanBulletPoint(line)
		if pattern == "" {
			continue
		}

		// Generate pattern name from first 50 chars
		patternName := me.generatePatternName(pattern, domain)

		patterns = append(patterns, MemoryPattern{
			Domain:        domain,
			PatternName:   patternName,
			PatternText:   pattern,
			PatternType:   patternType,
			SourceFile:    sourceFile,
			SourceSection: sectionHeader,
			ExtractedAt:   time.Now().UTC(),
		})
	}

	return patterns
}

// extractGotchas finds ⚠️ marked items
func (me *MemoryExtractor) extractGotchas(content, sourceFile, domain string) []MemoryPattern {
	var patterns []MemoryPattern

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if !strings.Contains(line, "⚠️") {
			continue
		}

		pattern := me.cleanBulletPoint(line)
		if pattern == "" {
			continue
		}

		patternName := me.generatePatternName(pattern, domain)

		patterns = append(patterns, MemoryPattern{
			Domain:        domain,
			PatternName:   patternName,
			PatternText:   pattern,
			PatternType:   "gotcha",
			SourceFile:    sourceFile,
			SourceSection: "gotcha",
			ExtractedAt:   time.Now().UTC(),
		})
	}

	return patterns
}

// extractDomain tries to infer domain from file path or frontmatter
func (me *MemoryExtractor) extractDomain(content string) string {
	// Look for domain in frontmatter
	if strings.Contains(content, "domain:") {
		re := regexp.MustCompile(`domain:\s*([^\n]+)`)
		matches := re.FindStringSubmatch(content)
		if len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}

	// Fallback to generic pattern
	if strings.Contains(content, "mcpproxy") {
		return "infrastructure"
	}
	if strings.Contains(content, "prompts-mcp") || strings.Contains(content, "Swarm") {
		return "prompts"
	}
	if strings.Contains(content, "FDOT") || strings.Contains(content, "D3") {
		return "ops"
	}
	if strings.Contains(content, "IAM") || strings.Contains(content, "spiritual") {
		return "knowledge"
	}

	return "general"
}

// cleanBulletPoint removes markdown formatting from bullet points
func (me *MemoryExtractor) cleanBulletPoint(line string) string {
	// Remove leading symbols
	line = strings.TrimPrefix(line, "- ")
	line = strings.TrimPrefix(line, "✅ ")
	line = strings.TrimPrefix(line, "🔲 ")
	line = strings.TrimPrefix(line, "📋 ")
	line = strings.TrimPrefix(line, "⚠️ ")

	// Remove trailing markdown links
	re := regexp.MustCompile(`\[\[.*?\]\]$`)
	line = re.ReplaceAllString(line, "")

	return strings.TrimSpace(line)
}

// generatePatternName creates a short name from pattern text
func (me *MemoryExtractor) generatePatternName(pattern, domain string) string {
	// Take first 50 chars, replace spaces with hyphens, lowercase
	name := strings.ToLower(pattern)
	if len(name) > 50 {
		name = name[:50]
	}

	// Remove special characters
	re := regexp.MustCompile(`[^a-z0-9\s]`)
	name = re.ReplaceAllString(name, "")

	// Replace spaces with hyphens
	name = strings.ReplaceAll(name, " ", "-")

	// Truncate to 40 chars for pattern name
	if len(name) > 40 {
		name = name[:40]
	}

	return name
}

// FileHash computes MD5 hash of a file
func FileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := md5.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
