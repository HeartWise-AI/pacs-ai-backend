import json
import unittest
from pathlib import Path


MODEL_ROOT = Path(__file__).resolve().parents[1]


class TestHemorrhageModelInfo(unittest.TestCase):
    def test_onboarding_questionnaire_answer_keys(self):
        model_info = json.loads((MODEL_ROOT / "data/model_info.json").read_text())
        questions = model_info["onboardingModelQuestionnaires"]

        self.assertEqual(
            [question["id"] for question in questions],
            [
                "ich_ivh_phe_fairness_blind_spot",
                "ich_ivh_phe_underrepresented_population",
                "ich_ivh_phe_bias_sources",
            ],
        )
        self.assertEqual(
            [question["type"] for question in questions],
            ["RADIO", "RADIO", "CHECKBOX"],
        )
        self.assertEqual(
            [question["correctAnswerIds"] for question in questions],
            [
                ["overall_dice_without_race_ethnicity_stratification"],
                ["pediatric_patients_under_18"],
                [
                    "older_generation_ct_scanners",
                    "unknown_demographic_representation",
                    "focal_intracerebral_hemorrhage_emphasis",
                ],
            ],
        )

        for question in questions:
            english_ids = {option["id"] for option in question["answerOptionsEn"]}
            french_ids = {option["id"] for option in question["answerOptionsFr"]}

            self.assertEqual(english_ids, french_ids)
            self.assertLessEqual(set(question["correctAnswerIds"]), english_ids)


if __name__ == "__main__":
    unittest.main()
