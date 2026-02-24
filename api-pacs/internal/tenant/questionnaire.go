package tenant

type Questionnaire struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	QuestionEn      string         `json:"questionEn"`
	QuestionFr      string         `json:"questionFr"`
	AnswerOptionsEn []AnswerOption `json:"answerOptionsEn"`
	AnswerOptionsFr []AnswerOption `json:"answerOptionsFr"`
}

type AnswerOption struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
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
			ID:         "q2",
			Type:       "RADIO",
			QuestionEn: "Do you have any existing medical conditions?",
			QuestionFr: "Avez-vous des conditions médicales existantes?",
			AnswerOptionsEn: []AnswerOption{
				{ID: "yes", Answer: "Yes"},
				{ID: "no", Answer: "No"},
				{ID: "prefer", Answer: "Prefer not to say"},
			},
			AnswerOptionsFr: []AnswerOption{
				{ID: "yes", Answer: "Oui"},
				{ID: "no", Answer: "Non"},
				{ID: "prefer", Answer: "Je préfère ne pas répondre"},
			},
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
			ID:         "q2",
			Type:       "RADIO",
			QuestionEn: "Would you recommend our service to others?",
			QuestionFr: "Recommanderiez-vous notre service à d'autres?",
			AnswerOptionsEn: []AnswerOption{
				{ID: "yes", Answer: "Yes"},
				{ID: "no", Answer: "No"},
				{ID: "maybe", Answer: "Maybe"},
			},
			AnswerOptionsFr: []AnswerOption{
				{ID: "yes", Answer: "Oui"},
				{ID: "no", Answer: "Non"},
				{ID: "maybe", Answer: "Peut-être"},
			},
		},
	},
}
