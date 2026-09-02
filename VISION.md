# ClinLang — Vision and Architecture

## What ClinLang is

ClinLang is a **deterministic language for writing clinical documentation**.

A clinician types shorthand. The engine compiles it into a structured model and
renders that model into whatever form is needed — a SOAP note, markdown, plain
text, JSON.

The nearest analogy is Markdown. Markdown does not know what a book is; it knows
headings, emphasis and lists, and it renders them faithfully every time.
ClinLang does not know what a disease is; it knows commands, measurements,
findings and orders, and it renders them faithfully every time.

A `.cln` file is **the author's record of what happened**. It is not advice, not
a recommendation, and not a medical device.

---

## What ClinLang is not

Not an EHR. Not a hospital information system. Not a patient management
platform, a billing system, a scheduling system, or a FHIR server. Not clinical
decision support, a diagnostic engine, or an AI medical assistant.

It will not tell anyone what to do.

---

## The two rules everything else follows from

Most design arguments in this project reduce to one of these. When a question is
genuinely hard, it is usually because it sits on one of these lines.

### Rule 1 — The engine records; it never evaluates

ClinLang writes down what a clinician wrote. It does not judge it.

It **may** report that a token could not be parsed, that a dose has no unit,
that a command appears twice, that a word is not in the vocabulary.

It **may not** report that a value is high, low, abnormal or concerning; rank
findings by importance; check interactions, allergies or dose limits; suggest a
diagnosis; or compute a risk score.

The test is not "is this useful?" — many forbidden things are useful. It is:

> **Is this a statement about the text, or about the patient?**

Statements about the text are in scope. Statements about the patient are not,
and building them would make this a regulated medical device in most
jurisdictions.

This rule lives in code, not only in prose. `ir.Grade` may hold a symbol and a
label and nothing else. No IR type may gain a field named `severity`,
`critical`, `abnormal`, `risk`, `interpretation`, `rank` or `score` — a test
fails if one does. Diagnostic ranges `CLN4xxx` (terminology validation) and
`CLN5xxx` (value plausibility) are permanently reserved and permanently empty.

The risk is not a deliberate decision to build decision support. It is a
well-meaning change two years from now that adds a warning icon to an unusual
value.

### Rule 2 — Core owns what changes the parse; plugins own what changes the reading

The language ships **no clinical vocabulary**. No drug names, no analytes, no
findings, no abbreviations. Those arrive as plugins.

The line is not "is this medical?" That question has no crisp answer and led
this project astray once already. The line is:

> **Does the parser need this to produce the correct tree?**

| Vocabulary | Needed to parse? | Owner |
|---|---|---|
| Units (`kg`, `mg`, `mmHg`) | Yes — `wt65kg` cannot tokenize without them | Language |
| Durations (`d`, `wk`, `mo`) | Yes — `pain+++4h`, and `6m` versus `34M` | Language |
| Intensity markers (`+++`, `--`) | Yes — a grammar production | Language |
| Dosing frequencies (`tds`, `prn`) | Yes — `rx` classifies arguments by them | Language |
| Routes (`po`, `iv`) | Yes — same | Language |
| Drug names | No — identity and display only | Plugin |
| Analytes (`hb`, `pco2`) | Only as a convenience — see below | Plugin |
| Findings, abbreviations, imaging | No | Plugin |

**The enforceable form:**

> Remove every plugin, and the AST must be identical.
> Display changes. Structure does not.

That is a property a test can assert, and it is asserted. It is why the boundary
can be trusted rather than merely claimed.

**Two places this leaks, stated plainly:**

*Analytes* do affect the parse — they are what stops `pco245` splitting into
`pco` + `245`. They are a plugin anyway, because an explicit colon (`pco2:45`)
always overrides the split. The lexicon is a convenience; the colon is the
documented escape hatch.

*`rx`* is the one core command needing clinical vocabulary to parse. It stays
core because prescribing is universal rather than specialty knowledge, and
because a documentation language that cannot record what was given is a poor
one. A deliberate exception, written down so it is not mistaken for an
oversight.

---

## Usability is a constraint, not a trade-off

A language that is pure but unpleasant to write has failed. These rank alongside
the two rules above, and no architectural principle overrides them.

**The shipped binary is batteries-included.** "Medicine is a plugin" describes
*structure*, never *what a user receives*. Download it, double-click, start
typing — the full vocabulary is there. Modularity exists so vocabulary can be
reasoned about, tested and replaced, not so that less is shipped.

**Typing speed is a first-class constraint.** It is why the project exists.
Prefer the form needing no shift key. Prefer glued tokens over punctuation.
`hb13.5` must always be less work than `hb: 13.5`. When a syntax decision is
close, the faster one wins.

**Never punish unknown vocabulary.** An unrecognised term is recorded and
rendered exactly as typed. Unknown commands still capture their data. An
unlisted assay must never vanish. Vocabulary improves display; it never gates
what may be written.

**Nothing blocks rendering.** Every document yields a complete tree and a
renderable note, however malformed. A half-typed line still produces useful
structure, because that is the normal state of a live editor. Diagnostics are
information, not failure.

**Zero configuration to start, full configuration when wanted.** Works offline,
with no config file and no workspace. Every vocabulary file is overridable, and
a workspace override layers *over* the shipped vocabulary rather than replacing
it.

**No silent substitution.** The engine stores exactly what was typed and expands
only for display, retaining both. A note must be able to show precisely what its
author entered — a medicolegal property, not a stylistic one.

---

## Determinism and durability

Determinism is the highest technical priority. Identical input produces
byte-identical output, always. No AI, no heuristics, no probability, and no
dependence on map iteration order, wall-clock time, locale or filesystem state
anywhere in the pipeline.

Ordering is source order throughout. Sorting findings by anything else would be
the engine deciding what matters, which Rule 1 forbids.

**Notes are archival.** A `.cln` file written today must render identically in
ten years. That obliges a versioning policy:

- `@clinlang 1.0` declares the language version; absent means 1.0.
- Additive grammar changes bump the minor version.
- **Any change to how existing valid input parses bumps the major version and
  requires the pragma to opt in.** No exceptions.
- Deprecations get a two-version window and a `CLN0xxx` diagnostic.

Without this, no parse rule can ever be improved without silently altering
records that already exist.

---

## Architecture

```
source text
   │
   ▼
 lexer      one table-driven lexer; no vocabulary, no unit table
   │        tokens carry spans and adjacency
   ▼
 parser     recursive descent, error-recovering, always yields a document
   │        AST is verbatim and positioned; nothing is normalized here
   ▼
 sema       the only stage permitted to consult the lexicon
   │        resolves concepts, units and timing; produces the IR
   ▼
  IR        normalized model: quantities with units, identified concepts,
   │        structured timing, a provenance span on every node
   ▼
backends    SOAP │ plain │ markdown │ JSON │ …
```

Each stage knows only the one below it. The parser's entire dependency on the
outside world is command shapes and a key list. Neither is clinical.

**There is deliberately no concrete syntax tree.** A CST earns its cost when a
formatter must rewrite source in place preserving every byte. The AST is already
fully positioned and the source is retained beside it, which is enough for
`clinlang fmt`. A CST here would be borrowed ceremony.

**Every node carries provenance.** A value in a rendered note can always be
traced to the characters that produced it. That is what makes precise editor
diagnostics and audit trails possible, and it is not optional.

---

## Plugins

A plugin declares a manifest and implements only the capabilities it needs:
grammar shapes, vocabulary, semantic analysis, completions, presentation.

**Additive only.** A plugin can never shadow a core command or redefine core
vocabulary. A collision is reported and core behaviour kept, so loading a
specialty can never change what an existing note means.

**Compatibility is declared.** Each manifest states the engine range it supports
and is refused outside it. Once plugins are third-party, that declaration is the
only thing between a core refactor and silent breakage.

**Dispatch is deterministic.** Plugins are consulted in registration order.
Where two supply the same term, the first registered wins.

**Conflicts need a policy before they need a fix.** Cardiology's `ca` (calcium)
and oncology's `ca` (cancer antigen) will collide. First-registered-wins is
deterministic but arbitrary. Before a third specialty plugin exists this needs a
real answer — most likely profile-scoped resolution, where the active `@profile`
decides.

---

## Validation

Validation means **language** validation.

| Valid | Invalid | Why |
|---|---|---|
| `bp 120/80` | `bp abc` | A ratio needs numbers |
| `rx metformin 500 bd` | `rx` | A prescription needs a medication |
| `hb 8.2` | — | Never flagged. The engine does not judge values |

Every diagnostic carries a stable code, a severity and a resolved position. None
describes the patient.

Severity describes the text: a warning means "this probably did not parse as you
intended", never "this is clinically concerning". Editors render accordingly —
no alarming styling on clinical content, ever.

---

## Outputs

Every output is a backend over the IR, and backends are pure: an IR in, bytes
out, no file access, no globals, no clock.

Shipping: **SOAP, plain, markdown, JSON**.
Planned: **HTML, PDF, timeline, prescription**.

Interoperability formats such as FHIR are **out of scope for now** but not
designed out. `ir.Coding` carries `System` and `Code` fields that are always
empty, because SNOMED CT, LOINC and RxNorm are separately licensed and cannot be
embedded in an MIT-licensed binary. Attaching a terminology pack later is
additive, not a remodelling.

---

## Known gaps, named honestly

Real absences, not oversights. Naming them stops them being rediscovered as
surprises.

**No temporal model.** Longitudinal documentation is a stated purpose and a
timeline is a planned output, yet there is no way to write *"hb was 9 yesterday,
11 today"*. `ir.Encounters[]` exists and is unused. This is the largest
functional gap, and it blocks the timeline backend entirely.

**No `act` command.** Recording what happened — admitted, operated, drain
removed, referred — is squarely in scope and squarely distinct from
recommending it. `act admit` says the patient was admitted. It does not exist
yet.

**No language server.** Diagnostics are served over HTTP and rendered in the web
editor. An LSP server would serve VS Code and Neovim from the same engine; the
diagnostic model was shaped so that conversion is a struct literal.

**No multi-patient documents.** A ward round is one file per patient. The IR
supports multiple encounters; nothing produces them.

**The bundled drug list is imperfect.** Roughly 300 of its 5,724 entries are
industrial chemicals rather than medicines, and common brand names are missing.
It wants replacing with a curated formulary.

---

## Two unresolved product questions

Not architecture decisions, and this document does not settle them.

**Attachments.** "Portable plain-text format" and "version controllable" sit
awkwardly beside binary image upload and serving. Recording an image *path* is a
text fact and clearly belongs. Hosting the bytes is application behaviour, not
language behaviour, and should be labelled as such or removed.

**Hosted multi-tenant mode.** "Offline-first", "a notebook for clinicians" and
"not a hospital information system" sit awkwardly beside per-user workspaces and
proxy-forwarded identity. Either a hosted deployment is explicitly in scope, or
that code should go.

---

## Evaluating a proposal

Ask, in order:

1. **Does it state something about the patient rather than the text?**
   If yes, it does not belong here, however useful it is.

2. **Does it require the core to learn clinical vocabulary?**
   If yes, it belongs in a plugin — unless removing every plugin would change
   the AST, which is the only justification for core vocabulary.

3. **Does it make writing slower?**
   If yes, it must buy something substantial. Speed is the product.

4. **Does it change how existing valid input parses?**
   If yes, it is a major version and needs the pragma.

5. **Can two runs disagree?**
   If yes, it is a bug, not a feature.

Feature count is not a goal. A small language that is predictable, fast to
write, and still renders the same note in ten years is worth more than a large
one that is none of those.
