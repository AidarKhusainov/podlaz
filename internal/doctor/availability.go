package doctor

import "fmt"

type commandCheck struct {
	check Check
	path  string
}

func commandAvailability(runner CommandRunner, command string, checkName string) (commandCheck, bool) {
	path, err := runner.LookPath(command)
	if err != nil {
		return commandCheck{
			check: Check{
				Name:     checkName,
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("%s not found", command),
			},
		}, false
	}

	return commandCheck{
		path: path,
		check: Check{
			Name:     checkName,
			Severity: SeverityOK,
			Message:  fmt.Sprintf("%s found at %s", command, path),
		},
	}, true
}
