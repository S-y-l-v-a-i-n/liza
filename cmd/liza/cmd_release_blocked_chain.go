package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liza-mas/liza/internal/brand"
	"github.com/liza-mas/liza/internal/jsonout"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/spf13/cobra"
)

var releaseBlockedChainCmd = &cobra.Command{
	Use:   "release-blocked-chain <task-id> --reason <reason>",
	Short: "Permit one more replacement of a BLOCKED task at the blocked-recovery cap, without pausing the run",
	Long: fmt.Sprintf(`Release the blocked-recovery cap for one BLOCKED task's current blocked
episode. Agents may replace a lineage's BLOCKED tasks at most %[1]d times; the
next replacement is refused until a human releases that episode. This command
is that release, as an alternative to resolving the %[2]s
circuit-breaker halt with analyze then resume, which pauses every agent.

The task must be BLOCKED and at the cap, and its current episode not yet
released. The release permits one further replacement of this task. It ends
with the episode: when the task's successor blocks, or the task itself blocks
again, the cap applies again. Other capped tasks stay capped.

The release is recorded as blocked_recovery_released history (with the reason,
the episode and the lineage) and in the activity log. It changes no status,
mode or circuit-breaker state; the task's next orchestrator wake shows it.
Identified agent sessions cannot use this command.`, models.MaxAgentBlockedRecoveries, models.BlockedReplacementChainPattern),
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) (retErr error) {
		defer func() {
			if isJSON(cmd) && retErr != nil && !errors.Is(retErr, jsonout.ErrAlreadyWritten) {
				_ = jsonout.WriteResult(os.Stdout, nil, nil, retErr)
				retErr = jsonout.ErrAlreadyWritten
			}
		}()
		if brand.LookupEnv(os.Getenv, "AGENT_ID").Value != "" {
			return cliValidationError("release-blocked-chain is operator-only; agent sessions cannot release the blocked-recovery cap")
		}
		reason, _ := cmd.Flags().GetString("reason")
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}
		result, err := ops.ReleaseBlockedChain(projectRoot, args[0], reason)
		if isJSON(cmd) {
			var warnings []string
			if result != nil {
				warnings = result.Warnings
			}
			return jsonout.WriteResult(os.Stdout, result, warnings, err)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "blocked-recovery cap released for %s (episode %s): one further replacement permitted\n",
			result.TaskID, result.BlockedAt.UTC().Format(time.RFC3339))
		for _, warning := range result.Warnings {
			fmt.Fprintln(cmd.ErrOrStderr(), warning)
		}
		return nil
	},
}

func init() {
	releaseBlockedChainCmd.Flags().String("reason", "", "The human decision permitting one more replacement (recorded in history)")
	_ = releaseBlockedChainCmd.MarkFlagRequired("reason")
	addJSONFlag(releaseBlockedChainCmd)
	rootCmd.AddCommand(releaseBlockedChainCmd)
}
