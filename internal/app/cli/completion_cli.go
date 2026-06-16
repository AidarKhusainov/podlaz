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
  tunwarden completion bash
  tunwarden completion zsh
  tunwarden completion fish

Generate shell completion definitions for stdout. The command is read-only: it
prints completion scripts and does not contact tunwardend, start Xray, mutate
networking, or require root. Bash completion may read local profile and
subscription IDs during interactive completion only.
`)
}

func printBashCompletion(w io.Writer) {
	fmt.Fprintf(w, `# bash completion for tunwarden
# static commands: %s
# static connection modes: %s
# static profile protocols: %s

_tunwarden()
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

complete -o default -F _tunwarden tunwarden
`, completionWords(completionTopLevelCommandNames()), completionWords(completionConnectionModeNames()), completionWords(completionProfileProtocolNames()))
}

func printZshCompletion(w io.Writer) {
	fmt.Fprintf(w, `#compdef tunwarden
# zsh completion for tunwarden

_tunwarden() {
  local context state state_descr line
  typeset -A opt_args

  _arguments -C \
    '1:command:->command' \
    '2:subcommand:->subcommand' \
    '*::argument:->argument'

  local command="${words[2]}"
  local subcommand="${words[3]}"
  local prev="${words[$((CURRENT - 1))]}"

  case "$state" in
    command)
      _values 'command' %s
      ;;
    subcommand)
      case "$command" in
        profile)
          _values 'profile subcommand' %s
          ;;
        subscription)
          _values 'subscription subcommand' %s
          ;;
        completion)
          _values 'shell' %s
          ;;
        help)
          _values 'help topic' %s
          ;;
      esac
      ;;
    argument)
      case "$prev" in
        --mode)
          _values 'mode' %s
          return
          ;;
        --protocol)
          _values 'protocol' %s
          return
          ;;
      esac
      case "$command $subcommand" in
        'profile add')
          _values 'profile add flag' --name --server --port --protocol
          ;;
        'profile list'|'profile show')
          _values 'profile flag' --json
          ;;
        'profile delete')
          _values 'profile delete flag' --yes
          ;;
        'subscription add')
          _values 'subscription add flag' --name --url
          ;;
        'subscription list'|'subscription show')
          _values 'subscription flag' --json
          ;;
      esac
      case "$command" in
        plan)
          _values 'plan flag' --mode --json
          ;;
        connect)
          _values 'connect flag' --mode
          ;;
        doctor)
          _values 'doctor flag' --core --xray --json
          ;;
        logs)
          _values 'logs flag' --follow -f --daemon --core --since
          ;;
        recover)
          _values 'recover flag' --execute --yes --json
          ;;
      esac
      ;;
  esac
}

_tunwarden "$@"
`, zshWords(completionTopLevelCommandNames()), zshWords(completionProfileCommandNames()), zshWords(completionSubscriptionCommandNames()), zshWords(completionShellNames()), zshWords(completionTopLevelCommandNames()), zshWords(completionConnectionModeNames()), zshWords(completionProfileProtocolNames()))
}

func printFishCompletion(w io.Writer) {
	fmt.Fprintf(w, `# fish completion for tunwarden

function __fish_tunwarden_needs_command
    set -l words (commandline -opc)
    test (count $words) -le 1
end

function __fish_tunwarden_using_command
    set -l words (commandline -opc)
    test (count $words) -ge 2; and test $words[2] = $argv[1]
end

function __fish_tunwarden_needs_subcommand
    set -l words (commandline -opc)
    test (count $words) -eq 2; and test $words[2] = $argv[1]
end

function __fish_tunwarden_using_subcommand
    set -l words (commandline -opc)
    test (count $words) -ge 3; and test $words[2] = $argv[1]; and test $words[3] = $argv[2]
end

complete -c tunwarden -f
complete -c tunwarden -n '__fish_tunwarden_needs_command' -a '%s'
complete -c tunwarden -n '__fish_tunwarden_needs_subcommand profile' -a '%s'
complete -c tunwarden -n '__fish_tunwarden_needs_subcommand subscription' -a '%s'
complete -c tunwarden -n '__fish_tunwarden_needs_subcommand completion' -a '%s'
complete -c tunwarden -n '__fish_tunwarden_needs_subcommand help' -a '%s'

complete -c tunwarden -n '__fish_tunwarden_using_subcommand profile add' -l name -x
complete -c tunwarden -n '__fish_tunwarden_using_subcommand profile add' -l server -x
complete -c tunwarden -n '__fish_tunwarden_using_subcommand profile add' -l port -x
complete -c tunwarden -n '__fish_tunwarden_using_subcommand profile add' -l protocol -x -a '%s'
complete -c tunwarden -n '__fish_tunwarden_using_subcommand profile list' -l json
complete -c tunwarden -n '__fish_tunwarden_using_subcommand profile show' -l json
complete -c tunwarden -n '__fish_tunwarden_using_subcommand profile delete' -l yes

complete -c tunwarden -n '__fish_tunwarden_using_subcommand subscription add' -l name -x
complete -c tunwarden -n '__fish_tunwarden_using_subcommand subscription add' -l url -x
complete -c tunwarden -n '__fish_tunwarden_using_subcommand subscription list' -l json
complete -c tunwarden -n '__fish_tunwarden_using_subcommand subscription show' -l json

complete -c tunwarden -n '__fish_tunwarden_using_command plan' -l mode -x -a '%s'
complete -c tunwarden -n '__fish_tunwarden_using_command plan' -l json
complete -c tunwarden -n '__fish_tunwarden_using_command connect' -l mode -x -a '%s'
complete -c tunwarden -n '__fish_tunwarden_using_command doctor' -l core
complete -c tunwarden -n '__fish_tunwarden_using_command doctor' -l xray -x
complete -c tunwarden -n '__fish_tunwarden_using_command doctor' -l json
complete -c tunwarden -n '__fish_tunwarden_using_command logs' -l follow -s f
complete -c tunwarden -n '__fish_tunwarden_using_command logs' -l daemon
complete -c tunwarden -n '__fish_tunwarden_using_command logs' -l core
complete -c tunwarden -n '__fish_tunwarden_using_command logs' -l since -x
complete -c tunwarden -n '__fish_tunwarden_using_command recover' -l execute
complete -c tunwarden -n '__fish_tunwarden_using_command recover' -l yes
complete -c tunwarden -n '__fish_tunwarden_using_command recover' -l json
`, completionWords(completionTopLevelCommandNames()), completionWords(completionProfileCommandNames()), completionWords(completionSubscriptionCommandNames()), completionWords(completionShellNames()), completionWords(completionTopLevelCommandNames()), completionWords(completionProfileProtocolNames()), completionWords(completionConnectionModeNames()), completionWords(completionConnectionModeNames()))
}

func completionWords(values []string) string {
	return strings.Join(values, " ")
}

func zshWords(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "'" + value + "'"
	}
	return strings.Join(quoted, " ")
}
