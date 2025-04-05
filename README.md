# LazyCommit

LazyCommit automates the generation of meaningful commit messages by leveraging AI. It examines your code changes (while intelligently excluding generated files) and produces descriptive commit messages that explain what changed and why.

## Features

- Supports both Git and Jujutsu version control systems
- Automatically detects which VCS you're using (preferring Jujutsu if both are available)
- Intelligently excludes generated files based on `.gitattributes` and common lock files
- Integrated with LLMs through the `llm` command-line tool
- Efficiently truncates large diffs using tiktoken to stay within model context limits
- Written in Go for performance and portability

## Prerequisites

- Go 1.24 or later
- Either Git or Jujutsu installed
- [The `llm` command-line tool](https://github.com/simonw/llm)

## Installation

### Using Go tools

```bash
# Clone the repository
git clone https://github.com/stayradiated/lazycommit.git

# Navigate to the project directory
cd lazycommit

# Install directly to your GOPATH/bin
go install
```

Make sure `$GOPATH/bin` is in your PATH to access the `lazycommit` command from anywhere.

## Usage

```bash
# Basic usage
lazycommit

# With additional context for the AI
lazycommit "This commit fixes the authentication bug we discussed yesterday"
```

## Configuration

LazyCommit can be configured through a config file or environment variables:

### Configuration File

LazyCommit looks for configuration files in the following locations (in order of precedence):

1. `$XDG_CONFIG_HOME/lazycommit/config.toml` (or `~/.config/lazycommit/config.toml` if XDG_CONFIG_HOME is not set)
2. `~/.lazycommit.toml`

Example configuration file:

```toml
# LazyCommit Configuration

# Maximum number of tokens in diff to send to LLM
max_diff_tokens = 12500

# Path to custom prompt template
prompt_path = "/path/to/your/template.txt"

# Specific model to use with the llm tool
model_name = "claude-3.7-sonnet"
```

### Environment Variables

Environment variables override settings from the config file:

1. `LAZYCOMMIT_MAX_TOKENS` - Maximum number of tokens in the diff
2. `LAZYCOMMIT_TEMPLATE` - Path to the prompt template
3. `LAZYCOMMIT_MODEL` - Specific model to use with the llm tool

### Prompt Template

LazyCommit uses a template for generating the system prompt sent to the LLM. If no custom template is specified, a default embedded template is used. The template uses Go's text/template syntax with two variables:
- `{{.Branch}}`: The current branch name
- `{{.UserContext}}`: Additional context provided by you

## How It Works

1. LazyCommit detects whether you're using Git or Jujutsu (preferring Jujutsu if both are available)
2. It extracts the current branch name
3. It generates a diff of the changes, excluding generated files (determined from `.gitattributes`) and common lock files
4. It truncates the diff to stay within token limits using tiktoken
5. The diff is processed by the AI to generate a meaningful commit message
6. The commit message is displayed for you to use

## Development

For development or testing without installation:

```bash
# Build the binary
go build -o lazycommit

# Run the local binary
./lazycommit
```

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
