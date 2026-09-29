package domain

import "testing"

func TestSanitizeClaimName(t *testing.T) {
	name, yes := "Mallory\x1b[2J\nforged‮", true
	n := GraphNode{ClaimedByName: &name, ClaimedByNameIsFallback: &yes}
	n.SanitizeClaimName()
	if *n.ClaimedByName != "Mallory[2J forged" || !*n.ClaimedByNameIsFallback {
		t.Fatalf("= %q, %v", *n.ClaimedByName, *n.ClaimedByNameIsFallback)
	}
	// A name with nothing printable left is unknown, never a stand-in.
	empty := "\x1b​"
	n = GraphNode{ClaimedByName: &empty, ClaimedByNameIsFallback: &yes}
	n.SanitizeClaimName()
	if *n.ClaimedByName != "" || *n.ClaimedByNameIsFallback {
		t.Fatalf("= %q, %v; want \"\", false", *n.ClaimedByName, *n.ClaimedByNameIsFallback)
	}
	var none GraphNode
	none.SanitizeClaimName()
	if none.ClaimedByName != nil || none.ClaimedByNameIsFallback != nil {
		t.Fatal("an unclaimed node grew a name")
	}

	s := ProcessingSession{ActorName: "‮", ActorNameIsFallback: true}
	s.SanitizeActorName()
	if s.ActorName != "" || s.ActorNameIsFallback {
		t.Fatalf("session = %+v", s)
	}
}
