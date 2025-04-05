package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/BurntSushi/toml"
	"github.com/tiktoken-go/tokenizer"
)

// loadConfig loads configuration from config files and environment variables
func loadConfig() (AppConfig, error) {
	// Start with defaults
	config := DefaultConfig

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return config, fmt.Errorf("could not determine home directory: %v", err)
	}

	// Check for config file in XDG locations
	xdgConfigHome := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfigHome == "" {
		xdgConfigHome = filepath.Join(homeDir, ".config")
	}

	// Config file locations to try, in order of precedence
	configPaths := []string{
		filepath.Join(xdgConfigHome, "lazycommit", "config.toml"),
		filepath.Join(homeDir, ".lazycommit.toml"),
	}

	// Try to load each config file in order
	var configLoaded bool
	for _, path := range configPaths {
		if fileExists(path) {
			if _, err := toml.DecodeFile(path, &config); err != nil {
				return config, fmt.Errorf("error loading config from %s: %v", path, err)
			}
			fmt.Fprintf(os.Stderr, "Loaded configuration from %s\n", path)
			configLoaded = true
			break
		}
	}

	if !configLoaded {
		fmt.Fprintf(os.Stderr, "No configuration file found, using defaults\n")
	}

	// Override with environment variables if set
	if envMaxTokens := os.Getenv("LAZYCOMMIT_MAX_TOKENS"); envMaxTokens != "" {
		if maxTokens, err := strconv.Atoi(envMaxTokens); err == nil {
			config.MaxDiffTokens = maxTokens
			fmt.Fprintf(os.Stderr, "Using max tokens from environment: %d\n", maxTokens)
		}
	}

	if envPromptPath := os.Getenv("LAZYCOMMIT_TEMPLATE"); envPromptPath != "" {
		config.PromptPath = envPromptPath
		fmt.Fprintf(os.Stderr, "Using template path from environment: %s\n", envPromptPath)
	}

	if envModelName := os.Getenv("LAZYCOMMIT_MODEL"); envModelName != "" {
		config.ModelName = envModelName
		fmt.Fprintf(os.Stderr, "Using model from environment: %s\n", envModelName)
	}

	return config, nil
}


// Config holds the configuration for LazyCommit
type Config struct {
	MaxDiffSize int
	PromptPath  string
	UserContext string
	IsGit       bool // true for git, false for jj
	ModelName   string
}

// AppConfig holds the configuration loaded from the config file or environment
type AppConfig struct {
	MaxDiffTokens int    `toml:"max_diff_tokens"`
	PromptPath    string `toml:"prompt_path"`
	ModelName     string `toml:"model_name"`
}

// DefaultConfig provides default values for the application
var DefaultConfig = AppConfig{
	MaxDiffTokens: 12500,
	PromptPath:    "",
	ModelName:     "",
}

// PromptData holds the data for rendering the prompt template
type PromptData struct {
	Branch      string
	UserContext string
}

// DefaultPromptTemplate is used when no template file is found
const DefaultPromptTemplate = `You are an expert programmer helping to write concise, informative git commit messages. 
The user will provide you with a git diff, and you will respond with ONLY a commit message.

Here are the characteristics of a good commit message:
- Start with a short summary line (50-72 characters)
- Use the imperative mood ("Add feature" not "Added feature")
- Optionally include a more detailed explanatory paragraph after the summary, separated by a blank line
- Explain WHAT changed and WHY, but not HOW (that's in the diff)
- Reference relevant issue numbers if applicable (e.g. "Fixes #123")

Current branch: {{.Branch}}
User context: {{.UserContext}}

Respond with ONLY the commit message, no additional explanations, introductions, or notes.`

func main() {
	// Check if llm command is installed
	if !commandExists("llm") {
		fmt.Printf("\033[0;31mError: 'llm' command is not installed. Please install it and try again.\033[0m\n")
		os.Exit(1)
	}

	// Determine if we're in a git or jj repository
	isGit := isGitRepo()
	isJJ := isJJRepo()

	if !isGit && !isJJ {
		fmt.Printf("\033[0;31mError: Neither Git nor Jujutsu repository detected.\033[0m\n")
		os.Exit(1)
	}
	
	// Prefer Jujutsu if both are available
	useGit := isGit && !isJJ

	// Get user context from command line arguments
	userContext := ""
	if len(os.Args) > 1 {
		userContext = os.Args[1]
	}

	// Load configuration from file and environment
	appConfig, err := loadConfig()
	if err != nil {
		fmt.Printf("\033[0;31mWarning: Error loading configuration: %v. Using defaults.\033[0m\n", err)
		appConfig = DefaultConfig
	}

	// Create config for commit message generation
	config := Config{
		MaxDiffSize: appConfig.MaxDiffTokens,
		PromptPath:  appConfig.PromptPath,
		UserContext: userContext,
		IsGit:       useGit,
		ModelName:   appConfig.ModelName,
	}

	// Generate and display commit message
	if useGit {
		fmt.Fprintf(os.Stderr, "Using Git for version control\n")
	} else {
		fmt.Fprintf(os.Stderr, "Using Jujutsu for version control\n")
	}
	
	err = generateCommitMessage(config)
	if err != nil {
		fmt.Printf("\033[0;31mError: %v\033[0m\n", err)
		os.Exit(1)
	}
}

// generateCommitMessage generates a commit message using the configured VCS and LLM
func generateCommitMessage(config Config) error {
	// Get the current branch name
	branch := getBranchName(config.IsGit)
	
	// Render the prompt template
	promptData := PromptData{
		Branch:      branch,
		UserContext: config.UserContext,
	}
	
	prompt, err := renderPromptTemplate(config.PromptPath, promptData)
	if err != nil {
		return fmt.Errorf("failed to render prompt template: %v", err)
	}

	// Get diff with excluded generated files
	diff, err := getDiff(config)
	if err != nil {
		return fmt.Errorf("failed to get diff: %v", err)
	}

	// Truncate diff if it's too long using tiktoken
	truncatedDiff, err := truncateDiff(diff, config.MaxDiffSize)
	if err != nil {
		// Fall back to raw diff if tokenization fails
		fmt.Fprintf(os.Stderr, "Warning: Failed to tokenize diff: %v. Using raw diff.\n", err)
		truncatedDiff = diff
	}

	// Generate commit message with LLM
	var llmCmd *exec.Cmd
	if config.ModelName != "" {
		// Use specified model if provided
		llmCmd = exec.Command("llm", "-m", config.ModelName, "-s", prompt)
		fmt.Fprintf(os.Stderr, "Using model: %s\n", config.ModelName)
	} else {
		// Otherwise let llm use its default model
		llmCmd = exec.Command("llm", "-s", prompt)
		fmt.Fprintf(os.Stderr, "Using default llm model\n")
	}
	
	llmCmd.Stdin = strings.NewReader(truncatedDiff)
	llmCmd.Stdout = os.Stdout
	llmCmd.Stderr = os.Stderr

	return llmCmd.Run()
}

// truncateDiff truncates the diff to a specified number of tokens
func truncateDiff(diff string, maxTokens int) (string, error) {
	// Get the CL100K tokenizer used by Claude models
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		return "", fmt.Errorf("failed to get tokenizer: %v", err)
	}

	// Encode the diff to get tokens
	tokens, _, err := enc.Encode(diff)
	if err != nil {
		return "", fmt.Errorf("failed to encode diff: %v", err)
	}

	// If the number of tokens is within limit, return the original diff
	if len(tokens) <= maxTokens {
		return diff, nil
	}

	// Truncate tokens to the maximum allowed
	truncatedTokens := tokens[:maxTokens]

	// Decode the truncated tokens back to text
	truncatedDiff, err := enc.Decode(truncatedTokens)
	if err != nil {
		return "", fmt.Errorf("failed to decode truncated tokens: %v", err)
	}

	// Add a notice about truncation
	truncatedDiff += "\n\n[Diff truncated due to size - showing first " + fmt.Sprintf("%d", maxTokens) + " tokens]"

	return truncatedDiff, nil
}

// renderPromptTemplate renders the prompt template with the given data
func renderPromptTemplate(templatePath string, data PromptData) (string, error) {
	var tmplContent string
	
	// Try to read the template file if path is provided
	if templatePath != "" && fileExists(templatePath) {
		content, err := os.ReadFile(templatePath)
		if err != nil {
			return "", fmt.Errorf("failed to read template file: %v", err)
		}
		tmplContent = string(content)
		fmt.Fprintf(os.Stderr, "Using template from: %s\n", templatePath)
	} else {
		// Use the default template if file doesn't exist or no path provided
		tmplContent = DefaultPromptTemplate
		fmt.Fprintf(os.Stderr, "Using default embedded template\n")
	}
	
	// Parse the template
	tmpl, err := template.New("prompt").Parse(tmplContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %v", err)
	}
	
	// Execute the template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %v", err)
	}
	
	return buf.String(), nil
}

// getDiff gets the diff from the VCS, excluding generated files
func getDiff(config Config) (string, error) {
	excludePatterns := []string{}

	// Always exclude common lock files
	commonExcludes := []string{
		"pnpm-lock.yaml",
		"yarn.lock",
		"package-lock.json",
	}

	// Add generated files from .gitattributes if using Git
	if config.IsGit && fileExists(".gitattributes") {
		generatedPatterns, err := getGeneratedFilesFromGitattributes()
		if err != nil {
			return "", fmt.Errorf("failed to parse .gitattributes: %v", err)
		}
		excludePatterns = append(excludePatterns, generatedPatterns...)
	}

	// Add common excludes
	excludePatterns = append(excludePatterns, commonExcludes...)

	// Get diff based on VCS
	var cmd *exec.Cmd
	if config.IsGit {
		args := []string{"diff", "--cached", "--", "."}
		for _, pattern := range excludePatterns {
			args = append(args, fmt.Sprintf(":(exclude)%s", pattern))
		}
		cmd = exec.Command("git", args...)
	} else {
		args := []string{"diff", "--git"}
		for _, pattern := range excludePatterns {
			args = append(args, fmt.Sprintf("~%s", pattern))
		}
		cmd = exec.Command("jj", args...)
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get diff: %v", err)
	}

	return string(output), nil
}

// getGeneratedFilesFromGitattributes parses .gitattributes to find generated files
func getGeneratedFilesFromGitattributes() ([]string, error) {
	file, err := os.Open(".gitattributes")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	re := regexp.MustCompile(`^([^#][^\s]+)\s+.*linguist-generated\s*=\s*true`)

	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			patterns = append(patterns, matches[1])
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return patterns, nil
}

// getBranchName gets the current branch name from Git or Jujutsu
func getBranchName(isGit bool) string {
	var cmd *exec.Cmd
	if isGit {
		cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	} else {
		cmd = exec.Command("jj", "log", "--no-graph", "-T", "local_bookmarks", "--limit", "1")
	}

	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

// isGitRepo checks if the current directory is a Git repository
func isGitRepo() bool {
	_, err := os.Stat(".git")
	if err == nil {
		return true
	}

	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	err = cmd.Run()
	return err == nil
}

// isJJRepo checks if the current directory is a Jujutsu repository
func isJJRepo() bool {
	cmd := exec.Command("jj", "status", "--quiet")
	err := cmd.Run()
	return err == nil
}

// commandExists checks if a command exists
func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
