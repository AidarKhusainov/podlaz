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

Generate shell completion definitions for stdout. The command is read-only: it
prints completion scripts and does not contact podlazd, start Xray, mutate
networking, or require root. Generated completion scripts support both the
canonical podlaz command and the packaged plz alias. Bash, zsh, and fish
completion may read local profile and subscription IDs during interactive
completion only.
`)
}

func printBashCompletion(w io.Writer) {
	fmt.Fprintf(w, `# bash completion for podlaz and plz
# static commands: %s
# static connection modes: %s
# static profile protocols: %s

_podlaz()
{
    local cur line value
    local -a runtime_lines runtime_values
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
            "")
                continue
                ;;
        esac
        value="${line%%$'\t'*}"
        runtime_values+=("$value")
    done

    if ((${#runtime_values[@]} > 0)); then
        COMPREPLY=( $(compgen -W "${runtime_values[*]}" -- "$cur") )
    fi
    return 0
}

complete -o default -F _podlaz podlaz plz
`, completionWords(completionTopLevelCommandNames()), completionWords(completionConnectionModeNames()), completionWords(completionProfileProtocolNames()))
}

func printZshCompletion(w io.Writer) {
	fmt.Fprintf(w, `#compdef podlaz plz
# zsh completion for podlaz and plz
# static commands: %s
# static connection modes: %s
# static profile protocols: %s

_podlaz() {
  local runtime_output line value description plain
  local -a runtime_lines plain_values described_values
  local cursor=$((CURRENT - 1))

  runtime_output="$("${words[1]}" __complete zsh "$cursor" "${words[@]}" 2>/dev/null)" || return 0
  runtime_lines=("${(@f)runtime_output}")

  for line in "${runtime_lines[@]}"; do
    case "$line" in
      :default-files)
        _files
        return
        ;;
      :no-files)
        continue
        ;;
      :no-space)
        continue
        ;;
      "")
        continue
        ;;
    esac

    value="${line%%$'\t'*}"
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

  if (( ${#plain_values[@]} > 0 )); then
    compadd -- "${plain_values[@]}"
  fi
}

_podlaz "$@"
`, completionWords(completionTopLevelCommandNames()), completionWords(completionConnectionModeNames()), completionWords(completionProfileProtocolNames()))
}

func printFishCompletion(w io.Writer) {
	fmt.Fprintf(w, `# fish completion for podlaz and plz
# static commands: %s
# static connection modes: %s
# static profile protocols: %s

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
        if string match -q -- '-*' "$line"
            continue
        end
        printf '%%s\n' "$line"
    end
end

function __fish_podlaz_needs_runtime_argument
    set -l words (commandline -opc)
    set -l current (commandline -ct)

    if test -n "$current"; and string match -q -- '-*' "$current"
        return 1
    end

    if test (count $words) -gt 0
        switch $words[-1]
            case --mode --protocol --name --server --port --url --xray --since
                return 1
        end
    end

    return 0
end

function __fish_podlaz_needs_files
    __fish_podlaz_runtime | string match -q ':default-files'
end

function __fish_podlaz_using_command
    set -l words (commandline -opc)
    test (count $words) -ge 2; and test $words[2] = $argv[1]
end

function __fish_podlaz_using_subcommand
    set -l words (commandline -opc)
    test (count $words) -ge 3; and test $words[2] = $argv[1]; and test $words[3] = $argv[2]
end

complete -c podlaz -f
complete -c podlaz -n '__fish_podlaz_needs_runtime_argument' -a '(__fish_podlaz_complete)'
complete -c podlaz -n '__fish_podlaz_needs_files' -F
complete -c plz -f
complete -c plz -n '__fish_podlaz_needs_runtime_argument' -a '(__fish_podlaz_complete)'
complete -c plz -n '__fish_podlaz_needs_files' -F

complete -c podlaz -n '__fish_podlaz_using_subcommand profile add' -l name -x
complete -c podlaz -n '__fish_podlaz_using_subcommand profile add' -l server -x
complete -c podlaz -n '__fish_podlaz_using_subcommand profile add' -l port -x
complete -c podlaz -n '__fish_podlaz_using_subcommand profile add' -l protocol -x -a '%s'
complete -c podlaz -n '__fish_podlaz_using_subcommand profile list' -l json
complete -c podlaz -n '__fish_podlaz_using_subcommand profile show' -l json
complete -c podlaz -n '__fish_podlaz_using_subcommand profile validate' -l mode -x -a '%s'
complete -c podlaz -n '__fish_podlaz_using_subcommand profile validate' -l json
complete -c podlaz -n '__fish_podlaz_using_subcommand profile delete' -l yes

complete -c podlaz -n '__fish_podlaz_using_subcommand subscription add' -l name -x
complete -c podlaz -n '__fish_podlaz_using_subcommand subscription add' -l url -x
complete -c podlaz -n '__fish_podlaz_using_subcommand subscription list' -l json
complete -c podlaz -n '__fish_podlaz_using_subcommand subscription show' -l json

complete -c podlaz -n '__fish_podlaz_using_command plan' -l mode -x -a '%s'
complete -c podlaz -n '__fish_podlaz_using_command plan' -l json
complete -c podlaz -n '__fish_podlaz_using_command connect' -l mode -x -a '%s'
complete -c podlaz -n '__fish_podlaz_using_command doctor' -l core
complete -c podlaz -n '__fish_podlaz_using_command doctor' -l xray -x
complete -c podlaz -n '__fish_podlaz_using_command doctor' -l json
complete -c podlaz -n '__fish_podlaz_using_command logs' -l follow -s f
complete -c podlaz -n '__fish_podlaz_using_command logs' -l daemon
complete -c podlaz -n '__fish_podlaz_using_command logs' -l core
complete -c podlaz -n '__fish_podlaz_using_command logs' -l since -x
complete -c podlaz -n '__fish_podlaz_using_command recover' -l execute
complete -c podlaz -n '__fish_podlaz_using_command recover' -l yes
complete -c podlaz -n '__fish_podlaz_using_command recover' -l json

complete -c plz -n '__fish_podlaz_using_subcommand profile add' -l name -x
complete -c plz -n '__fish_podlaz_using_subcommand profile add' -l server -x
complete -c plz -n '__fish_podlaz_using_subcommand profile add' -l port -x
complete -c plz -n '__fish_podlaz_using_subcommand profile add' -l protocol -x -a '%s'
complete -c plz -n '__fish_podlaz_using_subcommand profile list' -l json
complete -c plz -n '__fish_podlaz_using_subcommand profile show' -l json
complete -c plz -n '__fish_podlaz_using_subcommand profile validate' -l mode -x -a '%s'
complete -c plz -n '__fish_podlaz_using_subcommand profile validate' -l json
complete -c plz -n '__fish_podlaz_using_subcommand profile delete' -l yes

complete -c plz -n '__fish_podlaz_using_subcommand subscription add' -l name -x
complete -c plz -n '__fish_podlaz_using_subcommand subscription add' -l url -x
complete -c plz -n '__fish_podlaz_using_subcommand subscription list' -l json
complete -c plz -n '__fish_podlaz_using_subcommand subscription show' -l json

complete -c plz -n '__fish_podlaz_using_command plan' -l mode -x -a '%s'
complete -c plz -n '__fish_podlaz_using_command plan' -l json
complete -c plz -n '__fish_podlaz_using_command connect' -l mode -x -a '%s'
complete -c plz -n '__fish_podlaz_using_command doctor' -l core
complete -c plz -n '__fish_podlaz_using_command doctor' -l xray -x
complete -c plz -n '__fish_podlaz_using_command doctor' -l json
complete -c plz -n '__fish_podlaz_using_command logs' -l follow -s f
complete -c plz -n '__fish_podlaz_using_command logs' -l daemon
complete -c plz -n '__fish_podlaz_using_command logs' -l core
complete -c plz -n '__fish_podlaz_using_command logs' -l since -x
complete -c plz -n '__fish_podlaz_using_command recover' -l execute
complete -c plz -n '__fish_podlaz_using_command recover' -l yes
complete -c plz -n '__fish_podlaz_using_command recover' -l json
`, completionWords(completionTopLevelCommandNames()), completionWords(completionConnectionModeNames()), completionWords(completionProfileProtocolNames()), completionWords(completionProfileProtocolNames()), completionWords(completionConnectionModeNames()), completionWords(completionConnectionModeNames()), completionWords(completionConnectionModeNames()), completionWords(completionProfileProtocolNames()), completionWords(completionConnectionModeNames()), completionWords(completionConnectionModeNames()), completionWords(completionConnectionModeNames()))
}

func completionWords(values []string) string {
	return strings.Join(values, " ")
}
