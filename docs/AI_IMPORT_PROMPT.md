# Prompt for converting IELTS tasks to import v1

Use one of the prompts below together with the raw task. The model must return
only the import text in a single `text` code block. Always run the result through
the admin preview before creating a draft.

## Reading

```text
Convert the IELTS Reading task below to the strict IELTS_READING_IMPORT_V1 format.

Requirements:
- Return only one text code block, with no commentary before or after it.
- Preserve every passage, instruction, question, option and answer; do not summarize.
- Use global unique question numbers and exact GROUP ranges.
- Use only these types: MULTIPLE_CHOICE, TRUE_FALSE_NOT_GIVEN,
  YES_NO_NOT_GIVEN, MATCHING_INFORMATION, MATCHING_HEADINGS,
  MATCHING_FEATURES, MATCHING_SENTENCE_ENDINGS, SENTENCE_COMPLETION,
  SUMMARY_COMPLETION, NOTE_COMPLETION, TABLE_COMPLETION,
  FLOW_CHART_COMPLETION, DIAGRAM_LABEL_COMPLETION, SHORT_ANSWER.
- Put passage prose under `### TEXT`.
- For completion questions put the question number in its blank: `{{17}}`.
- Put shared matching options under `options:` as `A: text`.
- Put all correct answers only under one final `## ANSWERS` section.
- Separate accepted spelling alternatives with ` | `.
- Add explanations under `## EXPLANATIONS` only when they are present in the source.
- Never invent missing questions, answers or explanations. Omit unknown answers;
  the import preview must report them as blocking errors for a human to fix.

Start with:
# IELTS_READING_IMPORT_V1
title: ...
exam_type: ACADEMIC or GENERAL
duration_minutes: 60

Raw task:
[PASTE HERE]
```

## Listening

```text
Convert the IELTS Listening task below to the strict IELTS_LISTENING_IMPORT_V1 format.

Requirements:
- Return only one text code block, with no commentary before or after it.
- Preserve every part, instruction, question, option and answer; do not summarize.
- Use global unique question numbers and exact GROUP ranges.
- Use only these types: MULTIPLE_CHOICE, MATCHING, MAP_LABELLING,
  PLAN_LABELLING, DIAGRAM_LABELLING, FORM_COMPLETION, NOTE_COMPLETION,
  TABLE_COMPLETION, FLOW_CHART_COMPLETION, SENTENCE_COMPLETION, SHORT_ANSWER.
- For completion questions put the question number in its blank: `{{17}}`.
- Put shared matching/labelling options under `options:` as `A: text`.
- Use `reuse_options: true` when the same option may answer multiple questions.
- Put all correct answers only under one final `## ANSWERS` section.
- Separate accepted spelling alternatives with ` | `.
- Add explanations under `## EXPLANATIONS` only when they are present in the source.
- Do not invent `audio_asset_id` or `image_asset_id`. Audio and images will be
  uploaded manually after import.
- Never infer content hidden in a missing map/image; omit the asset ID and preserve
  only the labels present in the source.
- Never invent missing questions or answers. Omit unknown answers so the import
  preview reports a blocking error for a human to fix.

Start with:
# IELTS_LISTENING_IMPORT_V1
title: ...
exam_type: ACADEMIC or GENERAL
duration_minutes: 40

Raw task:
[PASTE HERE]
```

The complete grammars and examples are in `READING_IMPORT_FORMAT.md` and
`LISTENING_IMPORT_FORMAT.md` in this directory.
