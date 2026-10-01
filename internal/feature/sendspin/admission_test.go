package sendspin

import "testing"

func roles(rs ...string) *[]string { return &rs }

func TestAllowedFollowsTheSpecTable(t *testing.T) {
	cases := []struct {
		cat      pskCategory
		acts     []string
		unpaired bool
		want     bool
	}{
		{categoryLongTerm, nil, false, true},
		{categoryLongTerm, []string{activityPlayback}, false, true},
		{categoryLongTerm, []string{activityPlayback, activityManagement}, false, true},
		{categoryLongTerm, []string{activityPairing}, false, true},
		{categoryLongTerm, []string{activityPairing, activityPlayback}, false, false},
		{categoryPairing, []string{activityPairing}, false, true},
		{categoryPairing, nil, false, false},
		{categoryPairing, []string{activityPlayback}, false, false},
		{categorySentinel, nil, false, true},
		{categorySentinel, []string{activityPairing}, false, true},
		{categorySentinel, []string{activityPlayback}, false, false},
		{categorySentinel, []string{activityPlayback}, true, true},
		{categorySentinel, []string{activityManagement}, true, false},
		{categorySentinel, []string{activityPlayback, activityPlayback}, true, false},
		{categorySentinel, []string{"dancing"}, true, false},
	}
	for _, c := range cases {
		if got := allowed(c.cat, c.acts, c.unpaired); got != c.want {
			t.Errorf("allowed(%v, %v, %v) = %v", c.cat, c.acts, c.unpaired, got)
		}
	}
}

func TestJudgeAppliesTheRulesInOrder(t *testing.T) {
	offered := []string{methodPairingPSK}
	pairingPSK := &activatePairing{Method: methodPairingPSK}
	pin := &activatePairing{Method: "dynamic_pairing_code", Format: "digits"}

	cases := []struct {
		name     string
		cat      pskCategory
		act      serverActivate
		unpaired bool
		want     verdict
	}{
		{"idle sentinel", categorySentinel, serverActivate{ActiveRoles: roles()}, false, verdict{}},
		{"unpaired playback allowed", categorySentinel, serverActivate{Activities: []string{activityPlayback}, ActiveRoles: roles(rolePlayer)}, true, verdict{}},
		{"unpaired playback refused", categorySentinel, serverActivate{Activities: []string{activityPlayback}, ActiveRoles: roles(rolePlayer)}, false, verdict{goodbye: goodbyePairingRequired}},
		{"roles on an idle sentinel", categorySentinel, serverActivate{ActiveRoles: roles(rolePlayer)}, false, verdict{goodbye: goodbyePairingRequired}},
		{"roles on a pairing connection", categorySentinel, serverActivate{Activities: []string{activityPairing}, ActiveRoles: roles(rolePlayer), Pairing: pin}, true, verdict{goodbye: goodbyeUnauthorized}},
		{"persisted roles are not judged", categoryLongTerm, serverActivate{Activities: []string{activityPairing}, Pairing: pairingPSK}, false, verdict{abort: abortMethodNotSupported}},
		{"paired playback", categoryLongTerm, serverActivate{Activities: []string{activityPlayback, activityManagement}, ActiveRoles: roles(rolePlayer)}, false, verdict{}},
		{"pairing psk on the pairing key", categoryPairing, serverActivate{Activities: []string{activityPairing}, ActiveRoles: roles(), Pairing: pairingPSK}, false, verdict{}},
		{"pairing psk on the sentinel", categorySentinel, serverActivate{Activities: []string{activityPairing}, ActiveRoles: roles(), Pairing: pairingPSK}, false, verdict{abort: abortMethodNotSupported}},
		{"code method not offered", categorySentinel, serverActivate{Activities: []string{activityPairing}, ActiveRoles: roles(), Pairing: pin}, false, verdict{abort: abortMethodNotSupported}},
		{"pairing without a method", categorySentinel, serverActivate{Activities: []string{activityPairing}, ActiveRoles: roles()}, false, verdict{abort: abortMethodNotSupported}},
		{"older draft names the method", categoryPairing, serverActivate{Activities: []string{activityPairing}, ActiveRoles: roles(), SelectedPairMethod: methodPairingPSK}, false, verdict{}},
		{"unknown activity", categoryLongTerm, serverActivate{Activities: []string{"dancing"}}, false, verdict{goodbye: goodbyeUnauthorized}},
	}
	for _, c := range cases {
		if got := judge(c.cat, c.act, c.unpaired, offered); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
