package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
)

var (
	completionTopLevelCommands     = []string{"version", "import", "profile", "subscription", "plan", "connect", "disconnect", "status", "doctor", "logs", "recover", "completion", "help"}
	completionProfileCommands      = []string{"add", "import", "list", "show", "delete"}
	completionSubscriptionCommands = []string{"add", "list", "show", "update"}
	completionShells               = []string{"bash", "zsh", "fish"}
	completionConnectionModes      = []string{planner.ModeProxyOnly, planner.ModeTun}
	completionProfileProtocols     = []string{"vless", "vmess", "trojan", "shadowsocks"}
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
only prints static command, subcommand, flag, and enum-value completion metadata
and does not contact tunwardend, read secrets, mutate networking, or require root.
`)
}

func printBashCompletion(w io.Writer) {
	fmt.Fprintf(w, `# bash completion for tunwarden

_tunwarden()
{
    local cur prev command subcommand
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev=""
    if (( COMP_CWORD > 0 )); then
        prev="${COMP_WORDS[COMP_CWORD-1]}"
    fi
    command="${COMP_WORDS[1]}"
    subcommand="${COMP_WORDS[2]}"

    case "$prev" in
        --mode)
            COMPREPLY=( $(compgen -W "%s" -- "$cur") )
            return 0
            ;;
        --protocol)
            COMPREPLY=( $(compgen -W "%s" -- "$cur") )
            return 0
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        case "$command $subcommand" in
            "profile add")
                COMPREPLY=( $(compgen -W "--name --server --port --protocol" -- "$cur") )
                return 0
                ;;
            "profile list"|"profile show")
                COMPREPLY=( $(compgen -W "--json" -- "$cur") )
                return 0
                ;;
            "profile delete")
                COMPREPLY=( $(compgen -W "--yes" -- "$cur") )
                return 0
                ;;
            "subscription add")
                COMPREPLY=( $(compgen -W "--name --url" -- "$cur") )
                return 0
                ;;
            "subscription list"|"subscription show")
                COMPREPLY=( $(compgen -W "--json" -- "$cur") )
                return 0
                ;;
        esac
        case "$command" in
            plan)
                COMPREPLY=( $(compgen -W "--mode --json" -- "$cur") )
                return 0
                ;;
            connect)
                COMPREPLY=( $(compgen -W "--mode" -- "$cur") )
                return 0
                ;;
            doctor)
                COMPREPLY=( $(compgen -W "--core --xray --json" -- "$cur") )
                return 0
                ;;
            logs)
                COMPREPLY=( $(compgen -W "--follow -f --daemon --core --since" -- "$cur") )
                return 0
                ;;
            recover)
                COMPREPLY=( $(compgen -W "--execute --yes --json" -- "$cur") )
                return 0
                ;;
        esac
    fi

    case "$COMP_CWORD" in
        1)
            COMPREPLY=( $(compgen -W "%s" -- "$cur") )
            return 0
            ;;
        2)
            case "$command" in
                profile)
                    COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                    return 0
                    ;;
                subscription)
                    COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                    return 0
                    ;;
                completion)
                    COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                    return 0
                    ;;
                help)
                    COMPREPLY=( $(compgen -W "%s" -- "$cur") )
                    return 0
                    ;;
            esac
            ;;
    esac
}

complete -F _tunwarden tunwarden
`, completionWords(completionConnectionModes), completionWords(completionProfileProtocols), completionWords(completionTopLevelCommands), completionWords(completionProfileCommands), completionWords(completionSubscriptionCommands), completionWords(completionShells), completionWords(completionTopLevelCommands))
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
`, zshWords(completionTopLevelCommands), zshWords(completionProfileCommands), zshWords(completionSubscriptionCommands), zshWords(completionShells), zshWords(completionTopLevelCommands), zshWords(completionConnectionModes), zshWords(completionProfileProtocols))
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
`, completionWords(completionTopLevelCommands), completionWords(completionProfileCommands), completionWords(completionSubscriptionCommands), completionWords(completionShells), completionWords(completionTopLevelCommands), completionWords(completionProfileProtocols), completionWords(completionConnectionModes), completionWords(completionConnectionModes))
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
