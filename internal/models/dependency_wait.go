package models

import (
	"slices"
	"strings"
)

// UnmetClaimDependencies is the dependency half of the claim predicate: every
// ordinary depends_on edge that is not directly MERGED, then every unmet
// provider prerequisite. Ordinary edges are canonicalized at mutation
// boundaries, so superseded targets are reported, not followed.
func UnmetClaimDependencies(task *Task, state *State, pr PipelineResolver) []DependencySatisfaction {
	if task == nil || state == nil {
		return nil
	}
	var unmet []DependencySatisfaction
	for _, depID := range task.DependsOn {
		depTask := state.FindTask(depID)
		if depTask == nil {
			unmet = append(unmet, DependencySatisfaction{
				DependencyID: depID,
				Kind:         DependencyInvalidMissing,
				Path:         []string{depID},
				BlockingIDs:  []string{depID},
			})
			continue
		}
		if depTask.Status == TaskStatusMerged {
			continue
		}
		kind := DependencyUnsatisfiedPending
		if depTask.Status == TaskStatusSuperseded {
			kind = DependencyUnsatisfiedSuperseded
		}
		unmet = append(unmet, DependencySatisfaction{
			DependencyID: depID,
			Kind:         kind,
			Path:         []string{depID},
			BlockingIDs:  []string{depID},
		})
	}
	return append(unmet, UnmetProviderDependencies(task, state.Tasks, pr)...)
}

// DependencyWait is a diagnostic read of what holds a task's admission: the
// claim predicate's unmet dependencies and the chain heads they lead to.
type DependencyWait struct {
	Unmet []DependencySatisfaction
	Heads []DependencyWaitHead
}

// DependencyWaitHead is where a wait currently stands: a task reached through
// blocking IDs that holds no dependency itself, a blocker that does not exist
// yet (empty Status), an unmet result naming no blocker, or a cycle. It is
// diagnosis, not an await target. Claimable is the role-level doer claim
// predicate; agent-level rejected-ownership leases are not considered.
type DependencyWaitHead struct {
	ID        string
	Status    TaskStatus
	Claimable bool
	// Reason is the reason on the edge that reached the head, or its kind.
	Reason string
}

// DependencyWaitResolver computes waits over one state snapshot, memoizing
// each task's unmet set across calls.
type DependencyWaitResolver struct {
	state *State
	pr    PipelineResolver
	unmet map[string][]DependencySatisfaction
}

// NewDependencyWaitResolver creates a resolver for one inspection pass.
func NewDependencyWaitResolver(state *State, pr PipelineResolver) *DependencyWaitResolver {
	return &DependencyWaitResolver{state: state, pr: pr, unmet: make(map[string][]DependencySatisfaction)}
}

// Wait returns nil for a nil or terminal task and for a task no dependency
// holds. Absence says nothing about status or ownership claimability.
func (r *DependencyWaitResolver) Wait(task *Task) *DependencyWait {
	if task == nil || task.Status.IsTerminal() {
		return nil
	}
	unmet := r.unmetFor(task)
	if len(unmet) == 0 {
		return nil
	}
	walk := waitWalk{resolver: r, path: []string{task.ID}, done: map[string]bool{}, seen: map[string]bool{}}
	walk.follow(unmet)
	return &DependencyWait{Unmet: unmet, Heads: walk.heads}
}

func (r *DependencyWaitResolver) unmetFor(task *Task) []DependencySatisfaction {
	if unmet, ok := r.unmet[task.ID]; ok {
		return unmet
	}
	unmet := UnmetClaimDependencies(task, r.state, r.pr)
	r.unmet[task.ID] = unmet
	return unmet
}

func (r *DependencyWaitResolver) claimable(task *Task) bool {
	if r.pr == nil {
		return false
	}
	doerRole, err := r.pr.DoerRole(task.RolePair)
	return err == nil && task.IsClaimable(doerRole, r.state.Tasks, r.pr)
}

// waitWalk is a depth-first walk over blocking IDs. path holds the tasks on
// the current branch so a revisit there is reported as a cycle; done tasks
// already contributed their heads. Bounded by the task count.
type waitWalk struct {
	resolver *DependencyWaitResolver
	path     []string
	done     map[string]bool
	seen     map[string]bool
	heads    []DependencyWaitHead
}

func (w *waitWalk) follow(unmet []DependencySatisfaction) {
	for _, result := range unmet {
		reason := result.Reason
		if reason == "" {
			reason = string(result.Kind)
		}
		if len(result.BlockingIDs) == 0 {
			head := DependencyWaitHead{ID: result.DependencyID, Reason: string(result.Kind)}
			if result.Reason != "" {
				head.Reason += ": " + result.Reason
			}
			if task := w.resolver.state.FindTask(result.DependencyID); task != nil {
				head.Status = task.Status
			}
			w.add(head)
			continue
		}
		for _, id := range result.BlockingIDs {
			w.visit(id, reason)
		}
	}
}

func (w *waitWalk) visit(id, reason string) {
	if at := slices.Index(w.path, id); at >= 0 {
		task := w.resolver.state.FindTask(id)
		cycle := append(slices.Clone(w.path[at:]), id)
		w.add(DependencyWaitHead{ID: id, Status: task.Status, Reason: "dependency cycle: " + strings.Join(cycle, " -> ")})
		return
	}
	if w.done[id] {
		return
	}
	w.done[id] = true
	task := w.resolver.state.FindTask(id)
	if task == nil {
		w.add(DependencyWaitHead{ID: id, Reason: reason})
		return
	}
	var unmet []DependencySatisfaction
	if !task.Status.IsTerminal() {
		unmet = w.resolver.unmetFor(task)
	}
	if len(unmet) == 0 {
		w.add(DependencyWaitHead{ID: id, Status: task.Status, Claimable: w.resolver.claimable(task), Reason: reason})
		return
	}
	w.path = append(w.path, id)
	w.follow(unmet)
	w.path = w.path[:len(w.path)-1]
}

func (w *waitWalk) add(head DependencyWaitHead) {
	if w.seen[head.ID] {
		return
	}
	w.seen[head.ID] = true
	w.heads = append(w.heads, head)
}
