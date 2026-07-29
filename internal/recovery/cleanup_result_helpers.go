package recovery

func recoveredWithMessage(candidate Candidate, message string) CleanupResult {
	return CleanupResult{Candidate: candidate, Status: "recovered", Message: message}
}
