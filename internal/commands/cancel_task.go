package commands

import (
	"fmt"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
)

// CancelTaskCommand cancels a task (transitions to ABANDONED) and prints the result to stdout.
// Delegates business logic to ops.CancelTask.
func CancelTaskCommand(projectRoot, taskID, reason, agentID string) error {
	result, err := ops.CancelTask(projectRoot, taskID, reason, agentID)
	return printCancelTaskResult(result, err)
}

// CancelTaskWithAuthorityCommand cancels a task using generation-fenced authority.
func CancelTaskWithAuthorityCommand(projectRoot, taskID, reason string, authority models.AgentAuthority) error {
	return CancelTaskWithAuthorityAndOptionsCommand(projectRoot, taskID, reason, authority, ops.LifecycleRequestOptions{})
}

// CancelTaskWithOptionsCommand cancels a task for an operator; agentID is the audit actor.
func CancelTaskWithOptionsCommand(projectRoot, taskID, reason, agentID string, request ops.LifecycleRequestOptions) error {
	result, err := ops.CancelTaskWithOptions(projectRoot, taskID, reason, agentID, request)
	return printCancelTaskResult(result, err)
}

func CancelTaskWithAuthorityAndOptionsCommand(projectRoot, taskID, reason string, authority models.AgentAuthority, request ops.LifecycleRequestOptions) error {
	result, err := ops.CancelTaskWithAuthorityAndOptions(projectRoot, taskID, reason, authority, request)
	return printCancelTaskResult(result, err)
}

func printCancelTaskResult(result *ops.CancelResult, err error) error {
	if err != nil {
		return fmt.Errorf("cancel task: %w", err)
	}

	if printLifecycleResult(result.LifecycleOutcome) {
		return nil
	}

	fmt.Printf("Cancelled task %s (was %s)\n", result.TaskID, result.OriginalStatus)
	return nil
}
