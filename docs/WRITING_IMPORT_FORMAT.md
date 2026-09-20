# IELTS Writing import format

Writing materials are imported as JSON. A material always contains exactly two tasks and is created as a draft. Images are uploaded separately in the Writing editor so that binary files are stored in MinIO rather than embedded in JSON.

## Academic example

```json
{
  "materials": [
    {
      "slug": "academic-writing-practice-1",
      "examType": "academic",
      "difficulty": "intermediate",
      "title": "Academic Writing Practice 1",
      "description": "Task 1 map and Task 2 opinion essay.",
      "durationMinutes": 60,
      "tasks": [
        {
          "type": "task1",
          "prompt": "The two maps below show road access to a city hospital in 2007 and 2010. Summarise the information by selecting and reporting the main features, and make comparisons where relevant.",
          "minimumWords": 150,
          "visualType": "map",
          "assessmentNotes": "The 2010 plan added roundabouts at both ends of Hospital Road. The roadside bus stops were replaced by a bus station west of Hospital Road. The former shared car park was divided into a staff car park south-east of the hospital and a larger public car park east of the Ring Road."
        },
        {
          "type": "task2",
          "prompt": "Living in a country where you have to speak a foreign language can cause serious social problems, as well as practical problems. To what extent do you agree or disagree?",
          "minimumWords": 250,
          "essayType": "opinion"
        }
      ]
    }
  ]
}
```

After import, open the draft, upload the Task 1 image, save, and publish. The editor writes the returned `visualAssetId` into the task automatically.

## Fields

- `examType`: `academic` or `general`.
- `difficulty`: `foundation`, `intermediate`, or `advanced`.
- `durationMinutes`: normally `60`.
- `type`: `task1` followed by `task2`.
- `minimumWords`: normally `150` and `250`.
- `visualType` for Academic Task 1: `bar_chart`, `line_graph`, `pie_chart`, `table`, `diagram`, `process`, `map`, or `mixed`.
- `assessmentNotes`: private factual reference for AI assessment. Include the main trends, stages, values, and comparisons visible in the image. It is never returned by student APIs. Do not write a model essay here.
- `essayType` for Task 2: `opinion`, `discussion`, `advantages_disadvantages`, `problem_solution`, or `two_part`.
- `letterTone` for General Task 1: `formal`, `semi-formal`, or `informal`.

`visualUrl` remains supported for legacy HTTPS images, but uploading to MinIO through the editor is preferred. Do not put base64 images or MinIO credentials in the import JSON.

## AI conversion instruction

When asking an AI to convert source material, use this instruction:

> Return only valid JSON matching the IELTS Writing import format. Preserve the original task wording. For an Academic Task 1 image, set `visualType` and create concise private `assessmentNotes` containing only objectively visible facts, main trends, values, stages, and comparisons. Do not invent unreadable values and do not include a model answer. Use `minimumWords` 150 for Task 1 and 250 for Task 2.
