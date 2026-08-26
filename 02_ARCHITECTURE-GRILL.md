# 02 — Architecture grill: Round 1 answers (captain → agent)

## Q1. Endgame for the Bun server (`packages/server`): replace / sidecar / peer

Replace with a go server, bun not needed

## Q2. Which of the unported routes are *product* vs Pulse/PAI residue

Describe this more, I do not understand what it does (you haiku agent can do this on your own, go retrieve info on this ask and update the page)

## Q3. Does `tools/relay` survive, or does its job fold into go-server + Go CLI

Describe this more, I do not understand what it does (you haiku agent can do this on your own, go retrieve info on this ask and update the page)

## Q4. Storage for search/audit given pure-stdlib constraint

Describe this more, I do not understand what it does (you haiku agent can do this on your own, go retrieve info on this ask and update the page)

## Q5. Beads-backed crew status: what does "backed" mean mechanically

It means that we use beads as the layer to tell the system of activity and health. All actions by agents are associated with beads:
- agent spawns - bead claimed
- agent struggles - robots bead created
- agent finished - bead closed

System can check all these states to get an idea of what is going on, and can also have various aliveness checks

## Q6. Auth: opt-in static bearer token vs strictly network-delegated

Tailscale is the security layer at the moment, whatever that means here

## Q7. Audit log fidelity vs redaction policy — which wins

Fidelity presently wins

## Q8. Webhook delivery contract: config, guarantees, event set

Describe this more, I do not understand what it does (you haiku agent can do this on your own, go retrieve info on this ask and update the page)

## Q9. Two front ends permanent; go-server as blessed panel host; deploy pipeline

Describe this more, I do not understand what it does (you haiku agent can do this on your own, go retrieve info on this ask and update the page)

## Q10. Public/private line in spawn layer (parlay-spawn, launchers, beads)

Describe this more, I do not understand what it does (you haiku agent can do this on your own, go retrieve info on this ask and update the page)
