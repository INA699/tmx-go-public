package fleet

import (
	"slices"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Caller is who makes a request: the owner, or one agent (by its token).
type Caller struct {
	Owner   bool
	AgentID string
	// Companion is the owner's desktop agent app through `tmx mcp --companion`: it may read the
	// fleet, Needs you and chats, and message the head (DESIGN.md "Companion"); the API lets it
	// reach nothing else. Never combined with Owner or AgentID.
	Companion bool
	// Peer is another fleet machine, authenticated by its pair token: it may launch, start,
	// stop, restart and interrupt agents here, read their (non-shell) screens, and type or press
	// keys into them and mail them the way a head agent does. It is never the owner: every
	// owner-only route and action refuses it, and it has no agent record here
	// (callerRecLocked refuses it, so every action that does not let peers in explicitly is
	// closed to it). Never combined with Owner, AgentID or Companion. Empty for everyone else.
	Peer string
	// PeerAs is whom the peer acts for on its own machine (its on-behalf agent name), "" for
	// the machine itself. Only with Peer.
	PeerAs string
}

// PeerMachineName is the mail name of a peer machine acting for itself (no on-behalf agent).
const PeerMachineName = "daemon"

// CompanionCaller is Companion (the link token).
var CompanionCaller = Caller{Companion: true}

// OwnerCaller is the human.
var OwnerCaller = Caller{Owner: true}

// AgentCaller is an agent acting through its token.
func AgentCaller(id string) Caller { return Caller{AgentID: id} }

// PeerCaller is another fleet machine, authenticated by its pair token, acting for itself.
func PeerCaller(host string) Caller { return Caller{Peer: host} }

// PeerCallerAs is another fleet machine acting for one of its own agents (name, as its on-behalf
// header says). An invalid or reserved name stands for the machine itself.
func PeerCallerAs(host, name string) Caller {
	if !model.ValidName(name) || ReservedAgentName(name) {
		name = ""
	}
	return Caller{Peer: host, PeerAs: name}
}

// ID is the caller as a message party: "owner", the agent ID, or for a peer machine its
// party "fleet:<name>@<host>" (the on-behalf agent, or "daemon" for the machine itself) - mail
// from a peer is labelled as the other machine's, never the owner's.
func (c Caller) ID() string {
	if c.Owner {
		return model.Owner
	}
	if c.Companion {
		return model.CompanionParty
	}
	if c.Peer != "" {
		name := c.PeerAs
		if name == "" {
			name = PeerMachineName
		}
		return PeerParty(name, c.Peer)
	}
	return c.AgentID
}

// CallerForToken maps an agent token (from its env) to its Caller.
func (f *Fleet) CallerForToken(token string) (Caller, bool) {
	if token == "" {
		return Caller{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.tokens[token]
	if !ok {
		return Caller{}, false
	}
	return AgentCaller(id), true
}

// callerRecLocked returns the calling agent's record (nil for the owner). A peer machine has no
// record here and is refused: a nil record means the owner, so the few actions open to peers
// (launch, act, screen, type, keys, mail) let them in explicitly, before calling this.
func (f *Fleet) callerRecLocked(c Caller) (*agentRec, error) {
	if c.Owner {
		return nil, nil
	}
	if c.Peer != "" {
		return nil, errf(KindForbidden, "another machine of the fleet (%s) cannot do this here", c.Peer)
	}
	if c.Companion {
		return nil, errf(KindForbidden, "Companion cannot do this in tmx")
	}
	r, ok := f.agents[c.AgentID]
	if !ok {
		return nil, errf(KindForbidden, "the calling agent no longer exists")
	}
	return r, nil
}

// Policy returns the per-level policy, derived from the built-in profiles.
func (f *Fleet) Policy() model.Policy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.policyLocked()
}

// SetPolicy writes a per-level policy into the built-in profiles (owner only): model, allowed
// models, permission mode, project creation and launch rights (a level in can_launch means
// its built-in profile; custom profiles already allowed for a level that stays are kept).
func (f *Fleet) SetPolicy(c Caller, p model.Policy) (model.Policy, error) {
	if !c.Owner {
		return model.Policy{}, errf(KindForbidden, "only the owner can change the policy")
	}
	if err := validatePolicy(p); err != nil {
		return model.Policy{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.profileListLocked()
	ps := make([]model.Profile, 0, len(levelOrder))
	for _, lv := range levelOrder {
		cur, ok := f.profiles[string(lv)]
		if !ok {
			continue
		}
		ps = append(ps, cur.Clone())
	}
	applyPolicy(ps, p, all)
	for _, prof := range ps {
		if err := f.validateProfileLocked(&prof); err != nil {
			return model.Policy{}, err
		}
	}
	for _, prof := range ps {
		prof.UpdatedAt = model.Now()
		if _, err := f.putProfileLocked(prof); err != nil {
			return model.Policy{}, err
		}
	}
	return f.policyLocked(), nil
}

func validatePolicy(p model.Policy) error {
	for _, lv := range []model.Level{model.LevelHead, model.LevelLead, model.LevelWorker} {
		lp, ok := p.Levels[lv]
		if !ok {
			return errf(KindInvalid, "the policy has no entry for level %s", lv)
		}
		if lp.Model == "" {
			return errf(KindInvalid, "the policy for %s has no default model", lv)
		}
		if !slices.Contains(lp.AllowedModels, lp.Model) {
			return errf(KindInvalid, "the default model of %s (%s) is not in its allowed models", lv, lp.Model)
		}
		for _, l := range lp.CanLaunch {
			if l != model.LevelLead && l != model.LevelWorker {
				return errf(KindInvalid, "%s cannot be allowed to launch %q agents", lv, l)
			}
		}
	}
	for lv := range p.Levels {
		if lv != model.LevelHead && lv != model.LevelLead && lv != model.LevelWorker {
			return errf(KindInvalid, "unknown level %q in policy", lv)
		}
	}
	return nil
}

// isDescendantLocked reports whether target sits below ancestor in the parent chain.
func (f *Fleet) isDescendantLocked(target *agentRec, ancestorID string) bool {
	seen := map[string]bool{}
	for id := target.a.ParentID; id != "" && !seen[id]; {
		if id == ancestorID {
			return true
		}
		seen[id] = true
		p, ok := f.agents[id]
		if !ok {
			return false
		}
		id = p.a.ParentID
	}
	return false
}

// authorizeActLocked: start/stop/restart/interrupt. The owner may act on anyone; a peer machine
// on any agent but a shell (the owner's own terminal); a head or lead on its own subtree;
// nobody on itself.
func (f *Fleet) authorizeActLocked(c Caller, target *agentRec) error {
	if c.Owner {
		return nil
	}
	if c.Peer != "" {
		if target.a.Harness == model.HarnessShell {
			return errf(KindForbidden, "only the owner can reach a shell")
		}
		return nil
	}
	if c.AgentID == target.a.ID {
		return errf(KindForbidden, "an agent cannot do this to itself")
	}
	me, err := f.callerRecLocked(c)
	if err != nil {
		return err
	}
	if me.a.Level == model.LevelWorker {
		return errf(KindForbidden, "workers cannot start or stop other agents")
	}
	if !f.isDescendantLocked(target, me.a.ID) {
		return errf(KindForbidden, "%s is not in %s's part of the tree", target.a.Name, me.a.Name)
	}
	return nil
}
