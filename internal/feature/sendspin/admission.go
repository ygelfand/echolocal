package sendspin

// The admissibility rules for server/activate: which activity sets a key permits, and what the room
// answers when a server asks for something its key does not allow. Pure functions, as the spec lays
// them out, so the table can be tested without a socket.

// verdict is what the room does with an activate: nothing, say goodbye and hang up, or abort the pairing
// and stay connected.
type verdict struct {
	goodbye string
	abort   string
}

func knownActivity(a string) bool {
	return a == activityPlayback || a == activityPairing || a == activityManagement
}

// validActivities is the structural check: every member known, none repeated.
func validActivities(acts []string) bool {
	seen := map[string]bool{}
	for _, a := range acts {
		if !knownActivity(a) || seen[a] {
			return false
		}
		seen[a] = true
	}
	return true
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// allowed is the spec's table of what each key permits a server to declare.
//
//	long-term: ['pairing'] alone, or any subset of {'playback', 'management'}
//	pairing:   ['pairing'] alone
//	sentinel:  [], ['pairing'], or ['playback'] when the room admits unpaired access
func allowed(cat pskCategory, acts []string, unpaired bool) bool {
	if !validActivities(acts) {
		return false
	}
	pairing := contains(acts, activityPairing)

	switch cat {
	case categoryLongTerm:
		if pairing {
			return len(acts) == 1
		}
		return true
	case categoryPairing:
		return pairing && len(acts) == 1
	case categorySentinel:
		switch {
		case len(acts) == 0:
			return true
		case len(acts) == 1 && pairing:
			return true
		case len(acts) == 1 && acts[0] == activityPlayback:
			return unpaired
		}
	}
	return false
}

// playbackCapable is whether the connection may carry roles: its activities, with playback added, are
// an allowed set.
func playbackCapable(cat pskCategory, acts []string, unpaired bool) bool {
	extended := acts
	if !contains(acts, activityPlayback) {
		extended = append(append([]string{}, acts...), activityPlayback)
	}
	return allowed(cat, extended, unpaired)
}

// judge applies the spec's rules to an activate, in order:
//
//  1. Sentinel key, unpaired access off, and turning it on would make the activation admissible: goodbye
//     'pairing_required'.
//  2. Activities the key does not allow, or roles named on a connection that cannot carry them: goodbye
//     'unauthorized'.
//  3. Pairing declared with a method the key disallows or the room did not offer: pair/abort
//     'method_not_supported', staying connected.
func judge(cat pskCategory, act serverActivate, unpaired bool, offered []string) verdict {
	if !validActivities(act.Activities) {
		return verdict{goodbye: goodbyeUnauthorized}
	}

	// Roles the message names have to fit what the key allows. Roles carried over from an earlier
	// activate do not: the spec has them treated as empty instead, which activated does.
	var roles []string
	if act.ActiveRoles != nil {
		roles = *act.ActiveRoles
	}

	fits := func(unpaired bool) bool {
		return allowed(cat, act.Activities, unpaired) &&
			(len(roles) == 0 || playbackCapable(cat, act.Activities, unpaired))
	}
	if !fits(unpaired) {
		if cat == categorySentinel && !unpaired && fits(true) {
			return verdict{goodbye: goodbyePairingRequired}
		}
		return verdict{goodbye: goodbyeUnauthorized}
	}

	if contains(act.Activities, activityPairing) {
		m := act.method()
		switch {
		case m == "":
			return verdict{abort: abortMethodNotSupported}
		case (m == methodPairingPSK) != (cat == categoryPairing):
			return verdict{abort: abortMethodNotSupported}
		case !contains(offered, m):
			return verdict{abort: abortMethodNotSupported}
		}
	}
	return verdict{}
}
