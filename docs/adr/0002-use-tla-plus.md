# ADR-0002: Use TLA+ for behavioral system specification

- Status: Accepted
- Date: 2026-09-01

## Context

The current bond exchange is deliberately small: sale offers for bonds belong
to users, and a user may buy one existing active offer. Its central correctness
risk is behavioral: several users may attempt to buy the same offer, but the
offer must produce at most one completed purchase, that purchase must identify
its buyer, and the offer must no longer be active.

The current model intentionally excludes buy offers, cancellation, matching,
balances, holdings, ownership transfer, settlement, retries, and failure
handling. Those behaviors are future scope only if introduced by an explicit
system decision.

At this stage, the system design is expected to change frequently. The formal
method should therefore support high-level abstraction, nondeterministic
behavior, rapid automated exploration, and useful counterexamples without
requiring a proof to be repaired after every design experiment.

## Decision

Use TLA+ as the primary language for executable, system-level behavioral
specifications.

Describe the system as initial states, state-transition actions, and temporal
properties. Keep action definitions separate from their top-level composition
where practical. Use TLC in local development and CI to exhaustively explore
small, finite model configurations. Check the currently declared safety
invariants, and add liveness properties only when the domain introduces a
corresponding progress requirement.

Every TLC configuration must make its bounds explicit. A successful TLC run is
evidence about the explored configuration, not a proof for arbitrary numbers
of traders, instruments, orders, messages, or failures.

TLA+ is not required to serve every verification purpose. Add a theorem prover
or implementation-oriented verifier if a future requirement cannot be met
economically by TLA+ and TLC. Such an addition should have its own ADR and a
defined relationship to the TLA+ specification.

## Consequences

- Concurrent and distributed behavior can be modeled independently of the
  eventual implementation architecture.
- Nondeterministic buyer and offer choices can be explored independently of a
  particular implementation schedule.
- The current model checks type correctness, unique active and purchased offer
  IDs, and disjoint active and purchased offers.
- Future safety or liveness properties can share the model when their domain
  behavior is intentionally introduced.
- TLC produces concrete counterexample traces that support iterative design.
- CI can continuously check representative bounded configurations.
- TLA+ has an unfamiliar mathematical and untyped notation, creating a
  learning cost and allowing some mistakes that a static type system would
  reject earlier.
- Explicit-state exploration is subject to state-space explosion. Models must
  use careful abstraction, small bounds, and symmetry where appropriate.
- Passing model checks does not prove unbounded correctness or establish that
  the production implementation conforms to the specification.
- Machine-checked proofs with TLAPS or another theorem prover require
  additional proof engineering and are not implied by this decision.

## Alternatives considered

### Alloy 6

Alloy provides temporal relational logic, bounded analysis through SAT-based
model finding, and strong visualization of generated structures and
counterexamples. It is especially effective when the central problem is a
network of structural relationships or configuration constraints.

It was not selected as the primary tool because the exchange's initial risk is
the ordering of competing buy actions and preservation of invariants across
state transitions. TLA+'s initial-state and next-state style maps directly to
those questions. Alloy remains a reasonable supplementary tool if complex
relational structure becomes the dominant modeling concern.

### Lean

Lean can express transition systems and produce universally quantified proofs
whose proof terms are checked by a small kernel. This offers substantially
stronger assurance than checking finite instances.

It was not selected for early system design because constructing and
maintaining proofs creates a higher feedback cost, and Lean does not provide
the same default workflow of automatically exploring behaviors and returning
short counterexample traces. Lean may be introduced for critical unbounded
theorems, financial algorithms, or refinement results that justify that cost.

### P

P models distributed systems as communicating state machines and explores
message and failure schedules. Its programming-language style and runtime
monitoring path can bring a model closer to an event-driven implementation.

It was not selected because it commits the model to communicating machines
earlier than necessary. TLA+ permits an atomic exchange abstraction first and
allows service, queue, and network boundaries to be introduced only when they
affect the properties under study. P should be reconsidered if the
implementation adopts actor-like state machines and model-to-runtime alignment
becomes a priority.

### Promela and SPIN

Promela and SPIN provide mature process, channel, and temporal-property model
checking. They are well suited to communication protocols.

They were not selected because TLA+ offers a more convenient high-level
mathematical state model for assets, orders, and refinement between abstract
and distributed designs. They remain an option for a narrowly scoped protocol
whose process and channel behavior is the primary concern.

### PlusCal

PlusCal is not a competing foundation: it translates algorithms into TLA+ and
uses the same checking tools. It may be used within this decision when an
algorithm is clearer in imperative pseudocode, while TLA+ remains the semantic
and verification layer.

## Reconsideration triggers

Revisit or supplement this decision when any of these conditions holds:

- required assurance extends beyond bounded exploration and justifies an
  unbounded, machine-checked proof;
- implementation conformance becomes more important than design exploration;
- relational structure, rather than behavior, becomes the dominant source of
  complexity;
- the system architecture standardizes on communicating state machines and a
  direct executable-model path is desired; or
- TLC cannot explore meaningful configurations after reasonable abstraction
  and symmetry reductions.

## References

- [A High-Level View of TLA+](https://lamport.azurewebsites.net/tla/high-level-view.html)
- [TLA+ Proof System](https://proofs.tlapl.us/doc/web/content/Home.html)
- [Alloy 6 language reference](https://alloytools.org/spec.html)
- [Lean language reference](https://lean-lang.org/doc/reference/latest/)
- [P language](https://p-org.github.io/P/)
- [SPIN model checker](https://spinroot.com/spin/whatispin.html)
