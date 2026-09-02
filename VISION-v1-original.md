# ClinLang Project Philosophy & Architecture

## Purpose

ClinLang is a **deterministic domain-specific language (DSL)** for writing structured clinical case documentation.

Its purpose is **not** to replace Electronic Health Records (EHRs), Hospital Information Systems (HIS), EMRs, or clinical workflow software.

Its purpose is also **not** to provide clinical decision support, diagnosis, treatment recommendations, guideline interpretation, drug interaction checking, or any feature that influences clinical decisions.

ClinLang is intended to function like **Markdown for medicine**.

Doctors should be able to write concise shorthand notes that remain:

* human readable
* deterministic
* structured
* searchable
* version controllable
* easily shareable
* compilable into different formats

A `.cln` file represents the author's documentation of a clinical encounter or case, not medical advice or a recommendation.

---

# Vision

Think of ClinLang as a programming language whose domain is clinical documentation.

The language itself should know almost nothing about medicine.

Medicine belongs in plugins.

The language provides:

* syntax
* grammar
* parsing
* AST
* validation
* formatting
* compilation

Plugins provide:

* terminology
* aliases
* templates
* specialty vocabularies
* rendering helpers

This separation is one of the fundamental architectural principles.

---

# What ClinLang IS

ClinLang is:

* a deterministic language
* a compiler project
* a documentation language
* a shorthand notation
* an offline-first tool
* a portable plain-text format
* a notebook for clinicians
* a structured case recording system
* a language suitable for version control (Git)
* a language suitable for collaborative documentation between clinicians

---

# What ClinLang IS NOT

ClinLang is NOT:

* an Electronic Health Record
* a Hospital Information System
* a patient management platform
* a hospital workflow engine
* a billing system
* a scheduling system
* a FHIR server
* a clinical decision support system
* an AI medical assistant
* a diagnostic engine
* a treatment recommendation engine
* a medical device

The compiler must never attempt to infer clinical decisions.

It only records what the author wrote.

---

# Core Philosophy

The language should answer questions like:

* Is this valid ClinLang syntax?
* What is the parsed AST?
* Can this be formatted?
* Can this be rendered?
* Can this be compiled?

The language must never answer questions like:

* What should the doctor do?
* Which treatment is best?
* Which diagnosis is more likely?
* What investigation should be ordered?

Those questions are outside the project's scope.

---

# Determinism

Determinism is the highest priority.

Every valid ClinLang document should always produce exactly the same Abstract Syntax Tree.

The compiler should never rely on AI, probability, heuristics, or ambiguity.

Two identical inputs must always generate identical outputs.

---

# Primary Use Cases

ClinLang is designed for:

* personal clinical notes
* personal case journals
* longitudinal case documentation
* educational case discussions
* resident teaching
* conference case presentations
* research case collections
* documentation shared between clinicians
* maintaining personal clinical archives

It is intended to replace:

* random Notepad files
* Word documents
* WhatsApp case summaries
* Telegram discussions
* handwritten shorthand notes

It is NOT intended to replace official hospital records.

---

# Clinical Workflow

ClinLang may document workflows.

For example:

* patient admitted
* surgery performed
* drain removed
* consultation requested
* medication changed
* follow-up planned

These are historical facts.

They represent events that occurred.

ClinLang should document them.

ClinLang should never recommend them.

Example:

Valid:

```
act admit
```

Meaning:

"The patient was admitted."

Invalid:

```
recommend admit
```

Meaning:

"The system recommends admission."

The second example is outside the project's scope.

---

# Language Design Principles

The language should be:

* deterministic
* minimal
* readable
* predictable
* composable
* compiler-friendly
* whitespace-oriented
* easy to parse
* easy to format

Grammar should remain small.

Avoid introducing special cases whenever possible.

Prefer a consistent syntax:

```
command arguments
```

instead of inventing command-specific syntax.

---

# Core Architecture

The architecture should resemble a modern compiler.

Text

↓

Lexer

↓

Parser

↓

Concrete Syntax Tree

↓

AST Builder

↓

Canonical AST

↓

Semantic Validation

↓

Compiler Backends

The AST is the single source of truth.

No backend should depend on parser internals.

---

# Plugins

The parser should never contain specialty-specific medical knowledge.

Plugins extend the language without changing the parser.

Plugins may register:

* terminology
* aliases
* snippets
* templates
* validators
* autocomplete entries
* compiler hooks
* rendering helpers

Examples:

Urology Plugin

Cardiology Plugin

Dermatology Plugin

Orthopedics Plugin

General Surgery Plugin

The parser should work even if every plugin is removed.

---

# Validation

Validation means language validation.

Examples:

Correct:

```
bp 120/80
```

Incorrect:

```
bp abc
```

Correct:

```
rx metformin 500 bd
```

Incorrect:

```
rx
```

Validation should detect malformed language.

Validation should not determine whether the treatment is medically appropriate.

---

# Outputs

The same AST should be renderable into multiple formats.

Possible outputs:

* Markdown
* HTML
* PDF
* Plain Text
* SOAP
* Timeline
* JSON

Every output is simply another compiler backend.

---

# Language Server Protocol for Editors

ClinLang should follow the Language Server Protocol (LSP) model.

Editors communicate with the ClinLang Language Server, which provides:

syntax highlighting
autocomplete
diagnostics
snippets
templates
document outline
formatting
semantic language features

# Long-Term Goal

ClinLang should become the canonical plain-text representation of structured clinical documentation.

A `.cln` file should be to clinical documentation what `.md` is to Markdown or `.go` is to Go source code.

It should remain:

* deterministic
* portable
* extensible
* offline
* human readable
* machine parsable

The project should always prioritize simplicity, predictability, and compiler correctness over feature count.

Whenever a new feature is proposed, evaluate it against one question:

> Does this help ClinLang document clinical facts more effectively without introducing clinical reasoning or decision-making?

If the answer is no, the feature probably does not belong in the core language.
