# IELTS Reading Import Format v1

This document is the source of truth for deterministic bulk Reading imports.
The format is an authoring representation: imported data is converted into the
existing versioned material, question-group, question, answer and explanation
model. Parsing never writes to the database; the separate confirmation request
creates draft materials atomically.

## Version and metadata

The first non-empty line must be exactly:

```text
# IELTS_READING_IMPORT_V1
```

Supported test metadata:

```text
title: IELTS Academic Reading Test 01
exam_type: ACADEMIC
duration_minutes: 60
```

- `title` is recommended. A missing title produces a warning and a fallback.
- `exam_type` accepts `ACADEMIC` or `GENERAL`, case-insensitively. When omitted,
  the value selected on the import page is used.
- `duration_minutes` is optional preview metadata. It is not currently persisted
  because the material domain has no test-level duration field.

## Passages

One or more passages may be supplied. There is no three-passage assumption.

```text
## PASSAGE 1
title: A synthetic passage
### TEXT
The complete passage text starts here and continues until the next GROUP,
PASSAGE, ANSWERS or EXPLANATIONS section.
```

Passage numbers must be positive. Passage text must satisfy the normal material
validation (currently at least 50 Unicode characters). A missing passage title
produces a warning and a generated fallback.

## Question groups

```text
### GROUP 1
range: 1-3
type: TRUE_FALSE_NOT_GIVEN
instruction:
Do the statements agree with the information in the passage?

1. First statement.
2. Second statement.
3. Third statement.
```

`range` and `type` are required. Every integer in the declared range must have
exactly one question, and no question may fall outside the range. Numbering is
global across the imported document. Non-contiguous numbering between groups
is allowed with a warning.

Canonical type values are the uppercase forms of the existing domain IDs:

| Import value | Stored ID |
|---|---|
| `MULTIPLE_CHOICE` | `multiple_choice` |
| `TRUE_FALSE_NOT_GIVEN` | `true_false_not_given` |
| `YES_NO_NOT_GIVEN` | `yes_no_not_given` |
| `MATCHING_INFORMATION` | `matching_information` |
| `MATCHING_HEADINGS` | `matching_headings` |
| `MATCHING_FEATURES` | `matching_features` |
| `MATCHING_SENTENCE_ENDINGS` | `matching_sentence_endings` |
| `SENTENCE_COMPLETION` | `sentence_completion` |
| `SUMMARY_COMPLETION` | `summary_completion` |
| `NOTE_COMPLETION` | `note_completion` |
| `TABLE_COMPLETION` | `table_completion` |
| `FLOW_CHART_COMPLETION` | `flow_chart_completion` |
| `DIAGRAM_LABEL_COMPLETION` | `diagram_label_completion` |
| `SHORT_ANSWER` | `short_answer` |

There is deliberately no separate `MATCHING_PEOPLE` domain type. People are
represented by `MATCHING_FEATURES` with person names as options.

## Answers

Answers are declared once, separately from visual question definitions:

```text
## ANSWERS
1: FALSE
2: NOT_GIVEN
3: accepted text
```

Whitespace is trimmed. Enum answers are normalized case-insensitively:
`true`, `True`, `NOT GIVEN` and `Not given` become their canonical values.
Option IDs are normalized to uppercase. Completion and short-answer text keeps
its meaningful spelling and case. Alternative accepted texts may be separated
with `|`, for example `organisation | organization`.

Every question needs an answer. Duplicate answers, unknown question numbers,
invalid enums and unknown option IDs are blocking errors.

Answer JSON written into the existing question model is:

- TFNG/YNNG: `{ "value": "NOT_GIVEN" }`;
- single multiple choice/matching: `{ "optionId": "B" }`;
- multiple-answer choice: `{ "optionIds": ["A", "C"] }`;
- completion/short answer: `{ "accepted": ["answer", "alternative"] }`.

## Multiple choice

Options immediately following a question belong to that question:

```text
### GROUP 2
range: 4-5
type: MULTIPLE_CHOICE
instruction:
Choose the correct letter, A, B, C or D.

4. Why was the study performed?
A: To verify a method
B: To publish a novel
C: To advertise equipment
D: To test listening

5. What happened next?
A: The work stopped
B: The test continued
C: The team left
D: Nothing changed
```

For a question with multiple correct choices, separate IDs with `|` in the
answer key, such as `4: A | C`.

## Matching and sentence endings

Shared options are placed before the numbered questions:

```text
### GROUP 3
range: 6-7
type: MATCHING_FEATURES
reuse_options: true
instruction:
Match each statement with the correct person.
options:
A: Alex
B: Blair
C: Casey

6. Recorded the initial result.
7. Repeated the experiment.
```

`reuse_options` is optional and defaults to `false`. The same representation is
used for `MATCHING_INFORMATION`, `MATCHING_HEADINGS` and
`MATCHING_SENTENCE_ENDINGS`. Options are persisted in `question.content.options`;
reuse is adapted to `question.content.reuse`.

## Completion blanks and answer limits

V1 source uses the global question number inside each placeholder:

```text
### GROUP 4
range: 8-9
type: NOTE_COMPLETION
answer_limit: ONE_WORD_AND_OR_NUMBER
instruction:
Complete the notes.

8. The sample contained {{8}}.
9. The work finished in {{9}}.
```

The parser checks that every completion question has exactly its own numbered
placeholder and converts it to the constructor representation `{{answer}}`.
Duplicate placeholders and placeholders outside the group range are errors.

A shared block is also supported:

```text
content:
Research notes
- sample: {{8}}
- completion year: {{9}}
```

Each line containing a placeholder becomes an existing question. The complete
block is retained as `question.content.context`.

Supported keyword limits:

- `ONE_WORD_ONLY`
- `ONE_WORD_AND_OR_NUMBER`
- `NO_MORE_THAN_TWO_WORDS`
- `NO_MORE_THAN_TWO_WORDS_AND_OR_NUMBER`
- `NO_MORE_THAN_THREE_WORDS`
- `NO_MORE_THAN_THREE_WORDS_AND_OR_NUMBER`

Structured syntax is equivalent:

```text
answer_limit:
  max_words: 2
  allow_number: true
```

Both forms become `question.content.completionRule` with `maxWords` and
`allowNumber`. The instruction remains presentation text, not the validation
source of truth.

## Explanations

Explanations are optional and may be multiline:

```text
## EXPLANATIONS
### 1
The first relevant sentence directly contradicts the statement.

The following paragraph confirms the same conclusion.
### 8
The passage uses the word "sample" in the relevant sentence.
```

An explanation is persisted in the existing versioned question row. Missing
explanations are information only, never a warning or error. An explanation for
an unknown question is an error.

## Formatting and validation rules

- LF and CRLF are accepted; input is Unicode.
- Structural markers and field names are case-insensitive, but the version
  header must identify V1 exactly.
- Section markers must be on their own line.
- Question syntax is `number. text` or `number) text`.
- Option syntax is `A: text` or `A. text`; option IDs are one uppercase letter.
- There is no escaping syntax in V1. Lines that resemble structural markers
  should not be used verbatim at the start of passage paragraphs.
- Errors block confirmation. Warnings allow it. Info reports counts only.
- A count other than 40 is allowed with a warning.
- Input without a version header is sent to the legacy parser for compatibility.
- Unknown `IELTS_READING_IMPORT_*` versions are rejected.

## Complete synthetic example

```text
# IELTS_READING_IMPORT_V1
title: Synthetic Academic Reading
exam_type: ACADEMIC
duration_minutes: 60

## PASSAGE 1
title: A synthetic bird study
### TEXT
Researchers observed a fictional bird in a protected forest. This original passage exists only to demonstrate deterministic Reading import.

### GROUP 1
range: 1-2
type: TRUE_FALSE_NOT_GIVEN
instruction:
Do the statements agree with the information?
1. The bird was observed in a protected forest.
2. The passage describes a real IELTS examination.

### GROUP 2
range: 3-3
type: NOTE_COMPLETION
answer_limit: ONE_WORD_ONLY
instruction:
Complete the note.
3. Researchers observed a fictional {{3}}.

## PASSAGE 2
title: A synthetic research team
### TEXT
Alex, Blair and Casey form a fictional research team. This second original passage is long enough to pass validation safely.

### GROUP 3
range: 4-5
type: MATCHING_FEATURES
reuse_options: true
instruction:
Match each statement with the correct researcher.
options:
A: Alex
B: Blair
C: Casey
4. Recorded the first observation.
5. Checked the observation twice.

### GROUP 4
range: 6-6
type: MULTIPLE_CHOICE
instruction:
Choose the correct letter.
6. Why was this passage written?
A: To provide an import example
B: To reproduce a real test
C: To advertise a product
D: To test listening

## ANSWERS
1: TRUE
2: FALSE
3: bird
4: A
5: B
6: A

## EXPLANATIONS
### 1
The passage explicitly places the observation in a protected forest.
### 3
The missing word in the passage is "bird".
### 6
The text is a synthetic import example.
```

