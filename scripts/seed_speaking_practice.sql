-- Idempotent shared-development seed for a complete IELTS-style Speaking test.
DO $$
DECLARE
    material_uuid UUID := '3ea47a91-7500-4cc3-bbd0-5d7a69b6dd01';
    version_uuid  UUID := '3ea47a91-7500-4cc3-bbd0-5d7a69b6dd02';
BEGIN
    IF EXISTS (
        SELECT 1 FROM speaking_materials
        WHERE slug = 'ielts-speaking-practice-learning-a-skill-1'
    ) THEN
        RAISE NOTICE 'Speaking practice material already exists; skipping';
        RETURN;
    END IF;

    INSERT INTO speaking_materials (
        id, slug, status, revision, current_version_number,
        published_version_id, published_at
    ) VALUES (
        material_uuid,
        'ielts-speaking-practice-learning-a-skill-1',
        'DRAFT',
        1,
        1,
        NULL,
        NULL
    );

    INSERT INTO speaking_material_versions (
        id, material_id, version_number, exam_type, difficulty,
        title, description, parts, created_by
    ) VALUES (
        version_uuid,
        material_uuid,
        1,
        'academic',
        'intermediate',
        'IELTS Speaking Practice: Learning a New Skill',
        'A complete three-part IELTS-style Speaking practice test about everyday routines, learning a skill, and education.',
        $json$
        [
          {
            "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d101",
            "position": 1,
            "type": "part1",
            "title": "Part 1 — Home, free time and learning",
            "instructions": "Answer each question naturally. Give short but developed answers of two or three sentences where possible.",
            "preparationSeconds": 0,
            "responseSeconds": 300,
            "cueCard": [],
            "questions": [
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d111",
                "position": 1,
                "prompt": "What do you like most about the place where you live?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d112",
                "position": 2,
                "prompt": "Is your hometown a good place for young people? Why or why not?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d113",
                "position": 3,
                "prompt": "What do you usually do in your free time?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d114",
                "position": 4,
                "prompt": "Do you prefer spending your free time alone or with other people?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d115",
                "position": 5,
                "prompt": "What kind of new skills would you like to learn in the future?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d116",
                "position": 6,
                "prompt": "Do you find it easier to learn from a teacher or by yourself?"
              }
            ]
          },
          {
            "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d201",
            "position": 2,
            "type": "part2",
            "title": "Describe a skill you learned that was useful to you",
            "instructions": "You have one minute to prepare. Speak for one to two minutes. You may make notes while preparing.",
            "preparationSeconds": 60,
            "responseSeconds": 120,
            "cueCard": [
              "what the skill was",
              "when and why you learned it",
              "how you learned it",
              "and explain how this skill has been useful to you"
            ],
            "questions": []
          },
          {
            "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d301",
            "position": 3,
            "type": "part3",
            "title": "Part 3 — Skills and education",
            "instructions": "Discuss the questions in greater depth. Support your opinions with reasons and examples.",
            "preparationSeconds": 0,
            "responseSeconds": 300,
            "cueCard": [],
            "questions": [
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d311",
                "position": 1,
                "prompt": "Which practical skills should all children learn at school?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d312",
                "position": 2,
                "prompt": "Do schools sometimes focus too much on academic subjects?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d313",
                "position": 3,
                "prompt": "How has technology changed the way people learn new skills?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d314",
                "position": 4,
                "prompt": "What are the advantages and disadvantages of learning through online videos?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d315",
                "position": 5,
                "prompt": "Why do some adults find it difficult to learn something completely new?"
              },
              {
                "id": "3ea47a91-7500-4cc3-bbd0-5d7a69b6d316",
                "position": 6,
                "prompt": "Do you think employers should provide more training for their workers?"
              }
            ]
          }
        ]
        $json$::jsonb,
        NULL
    );

    UPDATE speaking_materials
    SET status = 'PUBLISHED',
        published_version_id = version_uuid,
        published_at = CURRENT_TIMESTAMP,
        revision = 2,
        updated_at = CURRENT_TIMESTAMP
    WHERE id = material_uuid;
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM speaking_materials m
        JOIN speaking_material_versions v ON v.id = m.published_version_id
        WHERE m.slug = 'ielts-speaking-practice-learning-a-skill-1'
          AND m.status = 'PUBLISHED'
          AND jsonb_array_length(v.parts) = 3
    ) THEN
        RAISE EXCEPTION 'Speaking practice seed validation failed';
    END IF;
END
$$;
