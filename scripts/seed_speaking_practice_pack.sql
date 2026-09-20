-- Idempotent shared-development seed for four original IELTS-style Speaking tests.
-- The prompts are original practice content, not copied from an official IELTS paper.
DO $$
DECLARE
    item JSONB;
    material_uuid UUID;
    version_uuid UUID;
BEGIN
    FOR item IN SELECT value FROM jsonb_array_elements($seed$
    [
      {
        "materialId": "4ea47a91-7500-4cc3-bbd0-5d7a69b6dd01",
        "versionId": "4ea47a91-7500-4cc3-bbd0-5d7a69b6dd02",
        "slug": "ielts-speaking-practice-travel-and-tourism-2",
        "difficulty": "intermediate",
        "title": "IELTS Speaking Practice: Travel and Tourism",
        "description": "A complete three-part practice test about local travel, a memorable journey, and tourism.",
        "parts": [
          {
            "id": "4ea47a91-7500-4cc3-bbd0-5d7a69b6d101", "position": 1, "type": "part1",
            "title": "Part 1 - Travel and weekends",
            "instructions": "Answer each question naturally. Give short but developed answers where possible.",
            "preparationSeconds": 0, "responseSeconds": 300, "cueCard": [],
            "questions": [
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d111","position":1,"prompt":"Do you often travel to other towns or cities?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d112","position":2,"prompt":"What kind of transport do you use most often?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d113","position":3,"prompt":"Do you prefer planning a trip carefully or being spontaneous?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d114","position":4,"prompt":"What do you usually do at weekends?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d115","position":5,"prompt":"Is there a place near your home that visitors should see?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d116","position":6,"prompt":"Would you like to travel more in the future?"}
            ]
          },
          {
            "id": "4ea47a91-7500-4cc3-bbd0-5d7a69b6d201", "position": 2, "type": "part2",
            "title": "Describe a journey that you remember well",
            "instructions": "You have one minute to prepare. Speak for one to two minutes. You may make notes while preparing.",
            "preparationSeconds": 60, "responseSeconds": 120,
            "cueCard": ["where you went", "who you travelled with", "what happened during the journey", "and explain why you remember this journey"],
            "questions": []
          },
          {
            "id": "4ea47a91-7500-4cc3-bbd0-5d7a69b6d301", "position": 3, "type": "part3",
            "title": "Part 3 - Tourism and transport",
            "instructions": "Discuss the questions in greater depth. Support your opinions with reasons and examples.",
            "preparationSeconds": 0, "responseSeconds": 300, "cueCard": [],
            "questions": [
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d311","position":1,"prompt":"Why do people enjoy travelling to unfamiliar places?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d312","position":2,"prompt":"How can tourism benefit a local community?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d313","position":3,"prompt":"What problems can too many tourists create?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d314","position":4,"prompt":"Should governments limit the number of visitors to certain places?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d315","position":5,"prompt":"How might people travel differently in the future?"},
              {"id":"4ea47a91-7500-4cc3-bbd0-5d7a69b6d316","position":6,"prompt":"Is international travel important for understanding other cultures?"}
            ]
          }
        ]
      },
      {
        "materialId": "5ea47a91-7500-4cc3-bbd0-5d7a69b6dd01",
        "versionId": "5ea47a91-7500-4cc3-bbd0-5d7a69b6dd02",
        "slug": "ielts-speaking-practice-technology-3",
        "difficulty": "advanced",
        "title": "IELTS Speaking Practice: Technology and Communication",
        "description": "A complete three-part practice test about devices, a useful digital service, and technology in society.",
        "parts": [
          {
            "id": "5ea47a91-7500-4cc3-bbd0-5d7a69b6d101", "position": 1, "type": "part1",
            "title": "Part 1 - Devices and communication",
            "instructions": "Answer each question naturally. Give short but developed answers where possible.",
            "preparationSeconds": 0, "responseSeconds": 300, "cueCard": [],
            "questions": [
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d111","position":1,"prompt":"Which electronic device do you use most often?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d112","position":2,"prompt":"What do you mainly use the internet for?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d113","position":3,"prompt":"Do you prefer calling people or sending messages?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d114","position":4,"prompt":"Was technology important in your childhood?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d115","position":5,"prompt":"Is it easy for you to learn how to use new apps?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d116","position":6,"prompt":"Do you ever take a break from your phone?"}
            ]
          },
          {
            "id": "5ea47a91-7500-4cc3-bbd0-5d7a69b6d201", "position": 2, "type": "part2",
            "title": "Describe a website or app that you find useful",
            "instructions": "You have one minute to prepare. Speak for one to two minutes. You may make notes while preparing.",
            "preparationSeconds": 60, "responseSeconds": 120,
            "cueCard": ["what it is", "when you started using it", "what you use it for", "and explain why it is useful to you"],
            "questions": []
          },
          {
            "id": "5ea47a91-7500-4cc3-bbd0-5d7a69b6d301", "position": 3, "type": "part3",
            "title": "Part 3 - Technology in society",
            "instructions": "Discuss the questions in greater depth. Support your opinions with reasons and examples.",
            "preparationSeconds": 0, "responseSeconds": 300, "cueCard": [],
            "questions": [
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d311","position":1,"prompt":"How has technology changed communication between generations?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d312","position":2,"prompt":"Why do some people find new technology difficult to use?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d313","position":3,"prompt":"Should children have limits on their screen time?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d314","position":4,"prompt":"What kinds of jobs are most likely to change because of artificial intelligence?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d315","position":5,"prompt":"Is face-to-face communication becoming less important?"},
              {"id":"5ea47a91-7500-4cc3-bbd0-5d7a69b6d316","position":6,"prompt":"Who should be responsible for protecting personal data online?"}
            ]
          }
        ]
      },
      {
        "materialId": "6ea47a91-7500-4cc3-bbd0-5d7a69b6dd01",
        "versionId": "6ea47a91-7500-4cc3-bbd0-5d7a69b6dd02",
        "slug": "ielts-speaking-practice-nature-and-cities-4",
        "difficulty": "intermediate",
        "title": "IELTS Speaking Practice: Nature and Cities",
        "description": "A complete three-part practice test about outdoor activities, a natural place, and greener cities.",
        "parts": [
          {
            "id": "6ea47a91-7500-4cc3-bbd0-5d7a69b6d101", "position": 1, "type": "part1",
            "title": "Part 1 - The outdoors",
            "instructions": "Answer each question naturally. Give short but developed answers where possible.",
            "preparationSeconds": 0, "responseSeconds": 300, "cueCard": [],
            "questions": [
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d111","position":1,"prompt":"Do you spend much time outdoors?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d112","position":2,"prompt":"What is your favourite season of the year?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d113","position":3,"prompt":"Are there many parks near where you live?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d114","position":4,"prompt":"Did you enjoy learning about nature at school?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d115","position":5,"prompt":"Do you keep any plants at home?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d116","position":6,"prompt":"Would you prefer to live in the countryside or a city?"}
            ]
          },
          {
            "id": "6ea47a91-7500-4cc3-bbd0-5d7a69b6d201", "position": 2, "type": "part2",
            "title": "Describe a natural place you enjoyed visiting",
            "instructions": "You have one minute to prepare. Speak for one to two minutes. You may make notes while preparing.",
            "preparationSeconds": 60, "responseSeconds": 120,
            "cueCard": ["where the place is", "when and why you went there", "what you did there", "and explain how you felt about this place"],
            "questions": []
          },
          {
            "id": "6ea47a91-7500-4cc3-bbd0-5d7a69b6d301", "position": 3, "type": "part3",
            "title": "Part 3 - Nature and urban development",
            "instructions": "Discuss the questions in greater depth. Support your opinions with reasons and examples.",
            "preparationSeconds": 0, "responseSeconds": 300, "cueCard": [],
            "questions": [
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d311","position":1,"prompt":"Why is access to nature important for people in cities?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d312","position":2,"prompt":"How can city governments encourage greener lifestyles?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d313","position":3,"prompt":"Should new buildings be required to include green spaces?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d314","position":4,"prompt":"Why are some people unwilling to change environmentally harmful habits?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d315","position":5,"prompt":"Can technology solve most environmental problems?"},
              {"id":"6ea47a91-7500-4cc3-bbd0-5d7a69b6d316","position":6,"prompt":"How might cities look different fifty years from now?"}
            ]
          }
        ]
      },
      {
        "materialId": "7ea47a91-7500-4cc3-bbd0-5d7a69b6dd01",
        "versionId": "7ea47a91-7500-4cc3-bbd0-5d7a69b6dd02",
        "slug": "ielts-speaking-practice-work-and-education-5",
        "difficulty": "foundation",
        "title": "IELTS Speaking Practice: Work and Education",
        "description": "A complete three-part practice test about study routines, a helpful teacher, and the future of education.",
        "parts": [
          {
            "id": "7ea47a91-7500-4cc3-bbd0-5d7a69b6d101", "position": 1, "type": "part1",
            "title": "Part 1 - Work and study",
            "instructions": "Answer each question naturally. Give short but developed answers where possible.",
            "preparationSeconds": 0, "responseSeconds": 300, "cueCard": [],
            "questions": [
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d111","position":1,"prompt":"Do you work or are you a student?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d112","position":2,"prompt":"What do you enjoy most about your work or studies?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d113","position":3,"prompt":"Is there anything you would like to change about it?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d114","position":4,"prompt":"At what time of day do you work or study best?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d115","position":5,"prompt":"Do you prefer working alone or in a group?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d116","position":6,"prompt":"What job would you like to do in the future?"}
            ]
          },
          {
            "id": "7ea47a91-7500-4cc3-bbd0-5d7a69b6d201", "position": 2, "type": "part2",
            "title": "Describe a teacher who helped you learn something important",
            "instructions": "You have one minute to prepare. Speak for one to two minutes. You may make notes while preparing.",
            "preparationSeconds": 60, "responseSeconds": 120,
            "cueCard": ["who the teacher was", "what they taught you", "how they helped you", "and explain why you remember this teacher"],
            "questions": []
          },
          {
            "id": "7ea47a91-7500-4cc3-bbd0-5d7a69b6d301", "position": 3, "type": "part3",
            "title": "Part 3 - Teachers and the future of education",
            "instructions": "Discuss the questions in greater depth. Support your opinions with reasons and examples.",
            "preparationSeconds": 0, "responseSeconds": 300, "cueCard": [],
            "questions": [
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d311","position":1,"prompt":"What qualities make someone a good teacher?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d312","position":2,"prompt":"How is teaching adults different from teaching children?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d313","position":3,"prompt":"Should schools teach more practical workplace skills?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d314","position":4,"prompt":"Will online learning ever replace classroom teaching?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d315","position":5,"prompt":"Why do people change careers later in life?"},
              {"id":"7ea47a91-7500-4cc3-bbd0-5d7a69b6d316","position":6,"prompt":"Who should pay for professional training: workers, employers, or governments?"}
            ]
          }
        ]
      }
    ]
    $seed$::jsonb)
    LOOP
        material_uuid := (item->>'materialId')::uuid;
        version_uuid := (item->>'versionId')::uuid;

        INSERT INTO speaking_materials (
            id, slug, status, revision, current_version_number,
            published_version_id, published_at
        ) VALUES (
            material_uuid, item->>'slug', 'DRAFT', 1, 1, NULL, NULL
        ) ON CONFLICT (slug) DO NOTHING;

        IF FOUND THEN
            INSERT INTO speaking_material_versions (
                id, material_id, version_number, exam_type, difficulty,
                title, description, parts, created_by
            ) VALUES (
                version_uuid, material_uuid, 1, 'academic', item->>'difficulty',
                item->>'title', item->>'description', item->'parts', NULL
            );

            UPDATE speaking_materials
            SET status = 'PUBLISHED',
                published_version_id = version_uuid,
                published_at = CURRENT_TIMESTAMP,
                revision = 2,
                updated_at = CURRENT_TIMESTAMP
            WHERE id = material_uuid;
        END IF;
    END LOOP;
END
$$;

DO $$
DECLARE
    material_count INTEGER;
    invalid_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO material_count
    FROM speaking_materials
    WHERE slug IN (
        'ielts-speaking-practice-travel-and-tourism-2',
        'ielts-speaking-practice-technology-3',
        'ielts-speaking-practice-nature-and-cities-4',
        'ielts-speaking-practice-work-and-education-5'
    );

    SELECT COUNT(*) INTO invalid_count
    FROM speaking_materials m
    JOIN speaking_material_versions v ON v.id = m.published_version_id
    WHERE m.slug IN (
        'ielts-speaking-practice-travel-and-tourism-2',
        'ielts-speaking-practice-technology-3',
        'ielts-speaking-practice-nature-and-cities-4',
        'ielts-speaking-practice-work-and-education-5'
    )
      AND (m.status <> 'PUBLISHED' OR jsonb_array_length(v.parts) <> 3);

    IF material_count <> 4 OR invalid_count <> 0 THEN
        RAISE EXCEPTION 'Speaking practice pack validation failed';
    END IF;
END
$$;
