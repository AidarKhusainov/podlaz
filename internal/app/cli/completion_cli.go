package cli

import (
	"fmt"
	"io"
	"strings"
)

func runCompletionCommand(args []string, stdout io.Writer) error {
	if isHelp(args) {
		printCompletionHelp(stdout)
		return nil
	}
	if len(args) != 1 {
		return usageError("completion requires exactly one shell: bash, zsh, or fish")
	}

	switch strings.ToLower(args[0]) {
	case "bash":
		printBashCompletion(stdout)
	case "zsh":
		printZshCompletion(stdout)
	case "fish":
		printFishCompletion(stdout)
	default:
		return usageError("unsupported completion shell %q", args[0])
	}
	return nil
}

func printCompletionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz completion bash
  podlaz completion zsh
  podlaz completion fish

Generate shell completion definitions for stdout. Completion is read-only and may
read local user-owned profile/subscription state. Profile completion prefers human
names; stable IDs are surfaced only when names are ambiguous. Both "podlaz" and
the packaged "plz" alias are supported.
`)
}

func printBashCompletion(w io.Writer) {
	fmt.Fprintf(w, `# bash completion for podlaz and plz
# commands: %s

_podlaz()
{
    local cur line value
    local -a runtime_lines values
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"

    compopt +o default 2>/dev/null || true
    compopt +o nospace 2>/dev/null || true

    if ! mapfile -t runtime_lines < <("${COMP_WORDS[0]}" __complete bash "$COMP_CWORD" "${COMP_WORDS[@]}" 2>/dev/null); then
        return 0
    fi

    for line in "${runtime_lines[@]}"; do
        case "$line" in
            :default-files)
                compopt -o default 2>/dev/null || true
                return 0
                ;;
            :no-files)
                compopt +o default 2>/dev/null || true
                continue
                ;;
            :no-space)
                compopt -o nospace 2>/dev/null || true
                continue
                ;;
            "") continue ;;
        esac
        value="${line%%%%$'\t'*}"
        [[ "$value" == "$cur"* ]] || continue
        values+=("$value")
    done
    COMPREPLY=("${values[@]}")
}
complete -o default -F _podlaz podlaz plz
`, completionWords(completionTopLevelCommandNames()))
}

func printZshCompletion(w io.Writer) {
	fmt.Fprintf(w, `#compdef podlaz plz
# zsh completion for podlaz and plz
# commands: %s

_podlaz() {
  local runtime_output line value description plain
  local -a runtime_lines plain_values described_values
  local cursor=$((CURRENT - 1))

  runtime_output="$("${words[1]}" __complete zsh "$cursor" "${words[@]}" 2>/dev/null)" || return 0
  runtime_lines=("${(@f)runtime_output}")

  for line in "${runtime_lines[@]}"; do
    case "$line" in
      :default-files) _files; return ;;
      :no-files|:no-space|"") continue ;;
    esac
    value="${line%%%%$'\t'*}"
    if [[ "$line" == *$'\t'* ]]; then
      description="${line#*$'\t'}"
      described_values+=("${value}:${description}")
    else
      plain_values+=("$value")
    fi
  done

  if (( ${#described_values[@]} > 0 )); then
    for plain in "${plain_values[@]}"; do
      described_values+=("$plain")
    done
    _describe -t podlaz-completions 'podlaz completion' described_values
    return
  fi
  (( ${#plain_values[@]} > 0 )) && compadd -- "${plain_values[@]}"
}

_podlaz "$@"
`, completionWords(completionTopLevelCommandNames()))
}

func printFishCompletion(w io.Writer) {
	fmt.Fprintf(w, `# fish completion for podlaz and plz
# commands: %s

function __fish_podlaz_runtime
    set -l words (commandline -opc)
    set -l current (commandline -ct)

    if test (count $words) -eq 0
        set words podlaz
    else if test -n "$current"
        if test "$words[-1]" != "$current"
            set -a words "$current"
        end
    else
        set -a words ""
    end

    set -l cursor (math (count $words) - 1)
    command $words[1] __complete fish "$cursor" $words 2>/dev/null
end

function __fish_podlaz_complete
    for line in (__fish_podlaz_runtime)
        if string match -q ':*' -- "$line"
            continue
        end
        printf '%%s\n' "$line"
    end
end

function __fish_podlaz_needs_files
    __fish_podlaz_runtime | string match -q ':default-files'
end

complete -c podlaz -f
complete -c podlaz -a '(__fish_podlaz_complete)'
complete -c podlaz -n '__fish_podlaz_needs_files' -F
complete -c plz -f
complete -c plz -a '(__fish_podlaz_complete)'
complete -c plz -n '__fish_podlaz_needs_files' -F
`, completionWords(completionTopLevelCommandNames()))
}

func completionWords(values []string) string {
	return strings.Join(values, " ")
}
