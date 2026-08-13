# IELTS Listening Import Format v1

This is the canonical deterministic authoring format for Listening tests. It is
converted into the existing Listening test → version → part → group → question
model. Parse only produces a preview; confirmation creates a DRAFT.

## Header and metadata

```text
# IELTS_LISTENING_IMPORT_V1
title: IELTS Academic Listening Test 01
exam_type: ACADEMIC
duration_minutes: 40
```

The header is required. `exam_type` is `ACADEMIC` or `GENERAL`.

## Parts and media

```text
## PART 1
title: Cookery class enquiry
audio_asset_id: 00000000-0000-0000-0000-000000000000
```

Tests may contain any reasonable number of parts; four is the IELTS norm. Audio
is optional during text import. The easiest workflow is import first, upload an
MP3/M4A/WAV/OGG/WebM in the constructor, then save. If an asset was already
uploaded, its UUID may be supplied as `audio_asset_id`.

## Groups

```text
### GROUP 1
range: 1-3
type: FORM_COMPLETION
answer_limit: ONE_WORD_OR_A_NUMBER
instruction:
Complete the form below.

1. Customer name: {{1}}
2. Number of guests: {{2}}
3. Discount: {{3}}
```

`range` and `type` are required. Question numbers are global and unique. Every
number in the range must exist exactly once.

Supported canonical types:

| Import value | Stored ID |
|---|---|
| `MULTIPLE_CHOICE` | `multiple_choice` |
| `MATCHING` | `matching` |
| `MAP_LABELLING` | `map_labelling` |
| `PLAN_LABELLING` | `plan_labelling` |
| `DIAGRAM_LABELLING` | `diagram_labelling` |
| `FORM_COMPLETION` | `form_completion` |
| `NOTE_COMPLETION` | `note_completion` |
| `TABLE_COMPLETION` | `table_completion` |
| `FLOW_CHART_COMPLETION` | `flow_chart_completion` |
| `SENTENCE_COMPLETION` | `sentence_completion` |
| `SHORT_ANSWER` | `short_answer` |

## Completion and shared context

Every completion question uses its numbered placeholder, for example `{{7}}`.
The parser verifies the number and converts it to the constructor's
`{{answer}}` representation.

For a form, table, notes or flow chart, a shared visual block may be supplied:

```text
content:
Cookery Classes
- focus: how to {{1}} seasonal products
- returning clients get a {{2}} discount
```

Each line containing a placeholder becomes one question, and the whole block is
retained as group context.

Answer-limit keywords:

- `ONE_WORD_ONLY`
- `ONE_WORD_OR_A_NUMBER`
- `ONE_WORD_AND_OR_NUMBER`
- `NO_MORE_THAN_TWO_WORDS`
- `NO_MORE_THAN_TWO_WORDS_AND_OR_NUMBER`
- `NO_MORE_THAN_THREE_WORDS`
- `NO_MORE_THAN_THREE_WORDS_AND_OR_NUMBER`

They become structured `maxWords`/`allowNumber` config; instruction text is not
the validation source of truth.

## Multiple choice

Options placed immediately after a question belong to that question:

```text
### GROUP 2
range: 4-5
type: MULTIPLE_CHOICE
instruction:
Choose the correct letter A, B or C.

4. Why did the speaker call?
A: To book a class
B: To cancel a visit
C: To request a map

5. What will happen next?
A: The caller will pay
B: A form will be sent
C: The class will start
```

## Matching and labelling

```text
### GROUP 3
range: 6-8
type: MATCHING
reuse_options: true
instruction:
Match each statement with the correct person.
options:
A: Alex
B: Blair
C: Casey

6. Recorded the result.
7. Repeated the experiment.
8. Wrote the report.
```

Map, plan and diagram labelling use the same `options:` syntax. Attach the image
after import in the constructor, or provide an existing UUID:

```text
image_asset_id: 00000000-0000-0000-0000-000000000000
```

## Answers and explanations

```text
## ANSWERS
1: Morgan
2: 4
4: A
6: B

## EXPLANATIONS
### 1
The caller clearly states the name Morgan.

This explanation may contain several lines.
### 4
The speaker says the purpose is to book a class.
```

Answers are required and separate from question presentation. Alternative text
answers use `|`, e.g. `center | centre`. Option IDs are normalized to uppercase.
Explanations are optional and versioned with questions.

Errors block import: missing/duplicate/unknown answers or questions, invalid
ranges/placeholders/types/asset UUIDs, and unknown option IDs. A non-standard
question count is only a warning. Missing explanations and media are allowed.

## Complete synthetic example

```text
# IELTS_LISTENING_IMPORT_V1
title: Synthetic Listening Demo
exam_type: ACADEMIC
duration_minutes: 40

## PART 1
title: Cookery class enquiry
### GROUP 1
range: 1-2
type: FORM_COMPLETION
answer_limit: ONE_WORD_OR_A_NUMBER
instruction:
Complete the form.
1. Class focus: how to {{1}} seasonal products
2. Returning clients receive a {{2}} discount

## PART 2
title: Traffic changes
### GROUP 2
range: 3-3
type: MULTIPLE_CHOICE
instruction:
Choose the correct letter.
3. Why are changes needed?
A: Accidents have risen
B: Traffic has increased
C: Vehicle types have changed

### GROUP 3
range: 4-5
type: MAP_LABELLING
instruction:
Choose the correct map label.
options:
A: North road
B: Market square
C: Station entrance
4. New traffic lights
5. Pedestrian crossing

## ANSWERS
1: choose
2: 20%
3: B
4: A
5: C

## EXPLANATIONS
### 3
The speaker says traffic volume has increased.
```

Media is deliberately outside the text payload. Uploading binary files through
the constructor keeps the format readable and suitable for generation by an AI.
