import json
import unittest
from pathlib import Path


MODEL_ROOT = Path(__file__).resolve().parents[1]


class TestEchoPrimeModelInfo(unittest.TestCase):
    def test_onboarding_questionnaire_answer_keys(self):
        model_info = json.loads((MODEL_ROOT / "data/model_info.json").read_text())
        questions = model_info["onboardingModelQuestionnaires"]

        self.assertEqual(
            [question["id"] for question in questions],
            [
                "echoprime_bias_sources",
                "echoprime_bias_reduction",
                "echoprime_fairness_blind_spot",
            ],
        )
        self.assertEqual(
            [question["type"] for question in questions],
            ["CHECKBOX", "RADIO", "RADIO"],
        )
        self.assertEqual(
            [question["correctAnswerIds"] for question in questions],
            [
                [
                    "mostly_single_institution",
                    "handheld_views_excluded",
                    "external_acquisition_shift",
                ],
                ["diverse_annotators"],
                ["poor_quality_older_patients"],
            ],
        )

        for question in questions:
            english_ids = {option["id"] for option in question["answerOptionsEn"]}
            french_ids = {option["id"] for option in question["answerOptionsFr"]}

            self.assertEqual(english_ids, french_ids)
            self.assertLessEqual(set(question["correctAnswerIds"]), english_ids)


if __name__ == "__main__":
    unittest.main()
