package tenant

type Questionnaire struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`
	QuestionEn      string   `json:"questionEn"`
	QuestionFr      string   `json:"questionFr"`
	AnswerOptionsEn []string `json:"answerOptionsEn"`
	AnswerOptionsFr []string `json:"answerOptionsFr"`
}

var OnboardingQuestionnaires = map[string][]Questionnaire{
	"PRE_SURVEY": {
		{
			ID:              "q1",
			Type:            "TEXT",
			QuestionEn:      "What is your current activity level?",
			QuestionFr:      "Quel est votre niveau d'activité actuel?",
			AnswerOptionsEn: nil,
			AnswerOptionsFr: nil,
		},
		{
			ID:              "q2",
			Type:            "RADIO",
			QuestionEn:      "Do you have any existing medical conditions?",
			QuestionFr:      "Avez-vous des conditions médicales existantes?",
			AnswerOptionsEn: []string{"Yes", "No", "Prefer not to say"},
			AnswerOptionsFr: []string{"Oui", "Non", "Je préfère ne pas répondre"},
		},
	},
	"POST_SURVEY": {
		{
			ID:              "q1",
			Type:            "TEXT",
			QuestionEn:      "How was your experience?",
			QuestionFr:      "Comment était votre expérience?",
			AnswerOptionsEn: nil,
			AnswerOptionsFr: nil,
		},
		{
			ID:              "q2",
			Type:            "RADIO",
			QuestionEn:      "Would you recommend our service to others?",
			QuestionFr:      "Recommanderiez-vous notre service à d'autres?",
			AnswerOptionsEn: []string{"Yes", "No", "Maybe"},
			AnswerOptionsFr: []string{"Oui", "Non", "Peut-être"},
		},
	},
}
