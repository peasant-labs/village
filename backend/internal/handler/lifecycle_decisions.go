package handler

type objectCleanupDecision struct {
	DeleteCandidate  bool
	DeleteSuperseded bool
	DeleteTarget     bool
	Reconcile        bool
}

func cleanupDecision(operation string, completion TransactionCompletion, installed bool) objectCleanupDecision {
	if completion == TransactionCommitAmbiguous {
		return objectCleanupDecision{Reconcile: true}
	}
	if completion == TransactionKnownRollback || !installed {
		return objectCleanupDecision{DeleteCandidate: operation == "create" || operation == "republish" || operation == "rewrite"}
	}
	switch operation {
	case "republish", "rewrite":
		return objectCleanupDecision{DeleteSuperseded: true}
	case "delete":
		return objectCleanupDecision{DeleteTarget: true}
	default:
		return objectCleanupDecision{}
	}
}

type freshReadAction string

const (
	freshReadReturnError freshReadAction = "return_error"
	freshReadReturn404   freshReadAction = "return_404"
	freshReadReturn304   freshReadAction = "return_304"
	freshReadReadOnce    freshReadAction = "read_once"
)

func decideFreshRead(objectMissing, authorized, descriptorChanged, etagMatches bool) freshReadAction {
	if !objectMissing || !descriptorChanged {
		return freshReadReturnError
	}
	if !authorized {
		return freshReadReturn404
	}
	if etagMatches {
		return freshReadReturn304
	}
	return freshReadReadOnce
}

func casOutcome(rowsAffected int64) CASOutcome {
	if rowsAffected == 1 {
		return CASInstalled
	}
	return CASStale
}
