import json
import unittest
from pathlib import Path


MODEL_ROOT = Path(__file__).resolve().parents[1]


class TestCathEFModelInfo(unittest.TestCase):
    def test_onboarding_questionnaire_answer_keys(self):
        model_info = json.loads((MODEL_ROOT / "data/model_info.json").read_text())
        questions = model_info["onboardingModelQuestionnaires"]

        self.assertEqual(
            [question["id"] for question in questions],
            [
                "cathef_bias_limitations",
                "cathef_generalizability",
                "cathef_fairness_concern",
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
                    "left_coronary_only",
                    "atrial_fibrillation_underrepresented",
                    "non_simultaneous_reference",
                ],
                ["single_academic_center"],
                ["similar_correlation_across_sexes"],
            ],
        )

        for question in questions:
            english_ids = {option["id"] for option in question["answerOptionsEn"]}
            french_ids = {option["id"] for option in question["answerOptionsFr"]}

            self.assertEqual(english_ids, french_ids)
            self.assertLessEqual(set(question["correctAnswerIds"]), english_ids)


if __name__ == "__main__":
    unittest.main()
