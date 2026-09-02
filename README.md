# ClinLang

**A deterministic language for clinical documentation.**

You type shorthand. ClinLang compiles it into a structured model and renders
that model as a note. Same input, same output, every time.

The nearest analogy is Markdown. Markdown does not know what a book is; it
knows headings, emphasis and lists, and renders them faithfully. ClinLang does
not know what a disease is; it knows commands, measurements, findings and
orders, and renders them faithfully.

> [!IMPORTANT]
> **ClinLang is not a medical device.** It records what a clinician wrote and
> never evaluates it — no diagnosis, no dosing, no interaction checking, no
> decision support of any kind. It will not tell anyone that a value is high,
> low or concerning. Every clinical judgement remains the clinician's. See
> [DISCLAIMER.md](DISCLAIMER.md).

---

## What it looks like

Write this:

```
pt 58M wt82kg ht178cm
cc chest pain since 4h
pmh dm2 htn
sx chestpain+++4h sob++
ros fever cough
vitals bp160/100 hr98 spo292 temp37.4c
lab hb13.2 trop0.8 na138 k4.1
ix cxr:patchy consolidation left base
rx aspirin 300mg po stat
rx paracetamol 1g qds prn po
dx inferior STEMI
```

Run `clinlang soap note.cln`:

```
──────────────────────────────────────────────────
Patient: 58y/M | Wt: 82 kg | Ht: 178 cm
──────────────────────────────────────────────────

S — SUBJECTIVE
─────────────────────────
Chief Complaint: chest pain since 4h
PMH            : Type 2 Diabetes Mellitus Hypertension
Symptoms       : Chest pain (very severe, 4 hours); Shortness of breath (severe)
Denies         : Fever; Cough

O — OBJECTIVE
─────────────────────────
Vitals         : Blood pressure 160/100 mmHg | Heart rate 98 bpm | Oxygen saturation 92% | Temperature 37.4 C
Labs           : Haemoglobin 13.2 g/dL | Troponin 0.8 | Sodium 138 mmol/L | Potassium 4.1 mmol/L
Imaging        : cxr patchy consolidation left base
Derived        : Body mass index 25.88 kg/m2 | Body surface area 2.01 m2

A — ASSESSMENT
─────────────────────────
Diagnosis      : inferior ST-Elevation MI

P — PLAN
─────────────────────────
  ▸ Rx: Aspirin 300 mg Orally, immediately
  ▸ Rx: Paracetamol 1 g Orally, four times daily, as needed

──────────────────────────────────────────────────
```

Eleven lines in, a complete note out. Note that `qds prn` produced **both**
facts — four times daily *and* as needed. They are independent, so neither
overwrites the other.

---

## Install

Go 1.26 or newer. There are no other dependencies.

```bash
go install ./cmd/clinlang
```

or build a static binary:

```bash
make build          # ./dist/clinlang
make build-all      # linux, macOS, windows — amd64 and arm64
```

No cgo, no third-party packages, one file per platform.

---

## Usage

```bash
clinlang soap     note.cln     # SOAP-structured note
clinlang run      note.cln     # plain note
clinlang markdown note.cln     # markdown
clinlang json     note.cln     # the structured model

clinlang check    note.cln     # diagnostics only
clinlang complete sx chest     # completions for an argument
clinlang plugins               # loaded plugins and their commands
clinlang backends              # available output formats
```

Notes go to **stdout**, diagnostics to **stderr**, so output pipes cleanly:

```bash
clinlang soap note.cln > note.txt
```

Flags: `--verbatim` renders exactly what was typed without expanding
abbreviations; `--quiet` suppresses diagnostics.

---

## Writing it

The language is line-oriented: a command, then its arguments.

**Glue values to keys.** No spaces, no punctuation, no shift key.

```
pt 58M wt82kg ht178cm      age, sex, weight, height
vitals bp160/100 hr98      blood pressure as a real ratio
lab hb13.2 na138 k4.1      analyte and value
```

**Trailing `+` and `-` mark intensity**, and a duration can follow:

```
sx chestpain+++4h          very severe, four hours
sx cough-- back-pain+2d    resolving; mild, two days
lab hiv- crp+++            negative; strongly positive
```

The same marks read differently by context — on a symptom `-` means improving,
on a result it means negative.

**`ros` records what was denied**, which is a different statement from a symptom
that is improving:

```
ros fever cough syncope
```

**A colon value runs to the next key**, so findings need no underscores:

```
pe cvs:s1s2 no murmurs rs:bibasal crackles abd:soft
ix cxr:patchy consolidation left lower lobe
```

**Prose commands take the rest of the line**: `cc`, `hpi`, `pmh`, `sh`, `fh`,
`dx`, `ddx`, `alg`, `day`.

**Specialties are opt-in:**

```
@profile obgyn
pt 28F ga:34w
vitals bp120/75 fhr:142
```

Anything ClinLang does not recognise is still recorded and rendered exactly as
typed. Unknown vocabulary is never an error, and unknown commands still capture
their data.

---

## Diagnostics

Every diagnostic has a stable code and an exact position:

```
$ clinlang check note.cln
note.cln:1:10: warning: ratio has no value after the slash [CLN1004]
1 | vitals bp140/
             ^^^^
```

Diagnostics describe the **text**, never the patient. A warning means "this
probably did not parse as you intended", never "this is clinically concerning".

---

## The structured model

`clinlang json` emits the intermediate representation the backends render from
— units resolved, concepts identified, every node traceable to its source span:

```json
{
  "kind": "medication",
  "code": { "text": "paracetamol", "concept_id": "drug.paracetamol" },
  "dose": { "value": 1, "unit": "g", "canonical": "g", "dimension": "mass" },
  "timing": { "code": "QDS", "count": 4, "period": 1, "period_unit": "d" },
  "as_needed": true
}
```

---

## Architecture

```
source → lexer → AST → sema → IR → backends
```

Each stage knows only the one below it.

| | |
|---|---|
| `pkg/lexer` `pkg/parser` `pkg/ast` | Syntax. Vocabulary-free. |
| `pkg/sema` `pkg/ir` | Meaning. The only stage that consults the lexicon. |
| `pkg/lexicon` | Notation: units, durations, dosing and route abbreviations. |
| `pkg/vocab` | Clinical vocabulary — supplied as a plugin, not by the language. |
| `pkg/plugin` `pkg/plugins/` | Extension API; `medicine` and `obgyn`. |
| `pkg/backend` | SOAP, plain, markdown, JSON. |
| `pkg/source` `pkg/diag` | Positions and diagnostics. |
| `pkg/clinlang` | The facade the CLI and embedders call. |

**The language ships no clinical vocabulary.** No drug names, no analytes, no
findings. Those arrive as plugins, and the boundary is enforced rather than
claimed: removing every plugin leaves the AST identical. Only display changes.

The shipped binary bundles the vocabulary, so none of this is visible to
someone who just wants to write a note.

See [VISION.md](VISION.md) for the principles behind these decisions and the
gaps that remain.

---

## Customising the vocabulary

Drop JSON overrides in a `.clinlang/` directory beside your notes, or in any
ancestor directory:

```
cases/
  .clinlang/
    drugs.json          your formulary
    abbreviations.json  your local shorthand
  admission.cln
```

Overrides **layer over** the shipped vocabulary rather than replacing it: terms
you define win, everything else is inherited. `$CLINLANG_CONFIG` and your user
config directory are also searched. None of this is required.

---

## Development

```bash
make test           # all packages
make race           # determinism under concurrency
make fuzz           # lexer and parser against arbitrary input
make check          # test + vet + gofmt
make update-golden  # accept intentional output changes, after review
```

Golden files cover every backend and the whole example corpus, so any change in
rendered output shows up as a reviewable diff.

---

## Status

Pre-1.0. The language and compiler are complete and tested; documentation will
be written for the 1.0 release.

Known gaps, described in full in [VISION.md](VISION.md): no temporal model for
longitudinal notes, no `act` command for recording events, no language server,
no multi-patient documents, and a bundled drug list that needs curating.

---

## License

MIT. See [LICENSE](LICENSE).
