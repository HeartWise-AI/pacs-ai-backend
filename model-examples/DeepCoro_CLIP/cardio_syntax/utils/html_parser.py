import logging
from io import StringIO
from typing import Dict
from datetime import datetime


class HTMLParser:    
    # System colors for detailed analysis
    SYSTEM_COLORS = {
        'rca': '#e67e22',  # Orange
        'lca': '#3498db',  # Blue
        'other': '#9b59b6'  # Purple
    }
    
    expected_keys = ['probability', 'recommendations']

    CSS_STYLES = """
                :root {
                    --primary-color: #3498db;
                    --secondary-color: #2980b9;
                    --background-color: #f8f9fa;
                    --text-color: #2c3e50;
                    --border-color: #e9ecef;
                    --status-color: #4ecdc4;
                    --warning-color: #e74c3c;
                    --normal-color: #27ae60;
                    --card-bg: #ffffff;
                }
                
                body {
                    font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
                    margin: 0;
                    padding: 20px;
                    background-color: var(--background-color);
                    color: var(--text-color);
                    line-height: 1.6;
                }
                
                .detection-results {
                    max-width: 1200px;
                    margin: 0 auto;
                    background-color: var(--card-bg);
                    border-radius: 12px;
                    box-shadow: 0 4px 6px rgba(0,0,0,0.1);
                    padding: 30px;
                    border: 1px solid var(--border-color);
                }
                
                .header {
                    text-align: center;
                    margin-bottom: 40px;
                    padding-bottom: 30px;
                    border-bottom: 3px solid var(--primary-color);
                }

                .header h1 {
                    color: var(--primary-color);
                    margin: 0 0 10px 0;
                    font-size: 32px;
                    font-weight: 700;
                }

                .header .subtitle {
                    color: var(--secondary-color);
                    font-size: 16px;
                    margin: 0;
                }
                
                .status-cards {
                    display: grid;
                    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
                    gap: 20px;
                    margin-top: 30px;
                }

                .status-card {
                    display: flex;
                    align-items: center;
                    padding: 20px;
                    background-color: var(--card-bg);
                    border-radius: 10px;
                    box-shadow: 0 2px 4px rgba(0,0,0,0.05);
                    border: 1px solid var(--border-color);
                }

                .status-card .card-content {
                    flex-grow: 1;
                }

                .status-card .card-title {
                    color: var(--primary-color);
                    font-size: 18px;
                    font-weight: 600;
                    margin-bottom: 5px;
                }

                .status-card .card-number {
                    font-size: 28px;
                    font-weight: bold;
                    color: var(--status-color);
                    margin-bottom: 5px;
                }

                .status-card .card-label {
                    font-size: 14px;
                    color: var(--secondary-color);
                    text-transform: uppercase;
                    font-weight: 500;
                }
                
                /* Dynamic color classes for status cards */
                .status-card.stenosis.red {
                    border-color: #e74c3c;
                    background: linear-gradient(135deg, #e74c3c15, #e74c3c05);
                }
                
                .status-card.stenosis.green {
                    border-color: #27ae60;
                    background: linear-gradient(135deg, #27ae6015, #27ae6005);
                }
                
                .status-card.calcif.red {
                    border-color: #e74c3c;
                    background: linear-gradient(135deg, #e74c3c15, #e74c3c05);
                }
                
                .status-card.calcif.green {
                    border-color: #27ae60;
                    background: linear-gradient(135deg, #27ae6015, #27ae6005);
                }
                
                .status-card.thrombus.red {
                    border-color: #e74c3c;
                    background: linear-gradient(135deg, #e74c3c15, #e74c3c05);
                }
                
                .status-card.thrombus.green {
                    border-color: #27ae60;
                    background: linear-gradient(135deg, #27ae6015, #27ae6005);
                }
                
                .status-card.cto.red {
                    border-color: #e74c3c;
                    background: linear-gradient(135deg, #e74c3c15, #e74c3c05);
                }
                
                .status-card.cto.green {
                    border-color: #27ae60;
                    background: linear-gradient(135deg, #27ae6015, #27ae6005);
                }
                
                /* Color the numbers based on status */
                .status-card.red .card-number {
                    color: #e74c3c;
                }
                
                .status-card.green .card-number {
                    color: #27ae60;
                }"""

    @staticmethod
    def _get_css_styles() -> str:
        """Get CSS styles for the HTML parser."""
        return HTMLParser.CSS_STYLES

    @staticmethod
    def _validate_input(results: Dict) -> bool:
        """Validate input data structure for the new format."""
        if not isinstance(results, dict):
            logging.error("Input results must be a dictionary")
            return False
        
        for expected_key in HTMLParser.expected_keys:
            if expected_key not in results:
                logging.error(f"Missing '{expected_key}' key in results")
                return False
        
        if not isinstance(results['probability'], dict):
            logging.error("Probability data must be a dictionary")
            return False
        
        if not isinstance(results['recommendations'], dict):
            logging.error("Recommendations data must be a dictionary")
            return False
        
        return True
        
    @staticmethod
    def _generate_recommendations_section(recommendations: Dict[str, str]) -> str:
        """Generate HTML for the clinical recommendations section."""
        recommendations_en = recommendations.get("en", "")
        recommendations_fr = recommendations.get("fr", "")
        
        en_section = f'<div style="margin: 15px 0;"><span style="font-weight: bold; color: #3498db; text-transform: uppercase; font-size: 13px; margin-bottom: 8px; display: block;">English Recommendations</span><div style="font-size: 16px; line-height: 1.6;">{recommendations_en}</div></div>' if recommendations_en else ''
        fr_section = f'<div style="margin: 15px 0;"><span style="font-weight: bold; color: #3498db; text-transform: uppercase; font-size: 13px; margin-bottom: 8px; display: block;">Recommandations Françaises</span><div style="font-size: 16px; line-height: 1.6;">{recommendations_fr}</div></div>' if recommendations_fr else ''
        
        return f"""
                <!-- Clinical Recommendations Section -->
                <div style="margin: 40px 0; padding: 25px; background: linear-gradient(135deg, #3498db15, #3498db05); border-left: 4px solid #3498db; border-radius: 8px;">
                    <h2 style="color: #2c3e50; margin: 0 0 20px 0; font-size: 24px; font-weight: 600;">Clinical Recommendations</h2>            
                    {en_section}
                    <div style="margin: 20px 0;"></div>
                    {fr_section}
                </div>"""
    
    @staticmethod
    def _generate_diagnosis_section(probability: Dict) -> str:
        """Generate HTML for the diagnosis section with individual cards for each probability category."""
        if not probability:
            return ""

        CATEGORY_COLORS = {
            'no_disease': ('#27ae60', '#27ae6015', '#27ae6005'),   # green
            'mild':       ('#3498db', '#3498db15', '#3498db05'),   # blue
            'moderate':   ('#e67e22', '#e67e2215', '#e67e2205'),   # orange
            'severe':     ('#e74c3c', '#e74c3c15', '#e74c3c05'),   # red
        }

        CATEGORY_LABELS = {
            'no_disease': 'No Disease',
            'mild': 'Mild (Low SYNTAX)',
            'moderate': 'Moderate (Intermediate SYNTAX)',
            'severe': 'Severe (High SYNTAX)',
        }

        CATEGORY_RECOMMENDATIONS = {
            'no_disease': 'No revascularization indicated.',
            'mild': 'PCI preferred if revascularization indicated.',
            'moderate': 'Heart Team discussion recommended.',
            'severe': 'CABG preferred. Urgent Heart Team referral.',
        }

        # Build cards
        cards = []
        for key, value in probability.items():
            category = value.get('category', 'no_disease')
            regression_value = value.get('regression', 0.0)

            primary, bg_start, bg_end = CATEGORY_COLORS.get(
                category, CATEGORY_COLORS['no_disease']
            )
            label = CATEGORY_LABELS.get(category, category.replace('_', ' ').title())
            rec = CATEGORY_RECOMMENDATIONS.get(category, '')

            card = f"""<div style="margin: 20px 0; padding: 20px; background: linear-gradient(135deg, {bg_start}, {bg_end}); border-left: 4px solid {primary}; border-radius: 8px;">
                        <h3 style="color: {primary}; margin: 0 0 15px 0; font-size: 20px; font-weight: 600;">{key}</h3>
                        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px;">
                            <span style="font-weight: 500; color: #2c3e50;">Category:</span>
                            <span style="font-weight: 600; color: {primary};">{label}</span>
                        </div>
                        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px;">
                            <span style="font-weight: 500; color: #2c3e50;">SYNTAX Score:</span>
                            <span style="font-weight: 600; color: {primary};">{regression_value:.1f}</span>
                        </div>
                        <div style="margin-top: 10px; padding: 8px 12px; background-color: {bg_start}; border-radius: 4px; font-size: 14px; color: {primary}; font-weight: 500;">
                            {rec}
                        </div>
                    </div>"""
            cards.append(card)

        cards_html = '\n'.join(cards)

        return f"""<!-- Diagnosis Section -->
<div style="margin: 40px 0; padding: 25px; background: linear-gradient(135deg, #f8f9fa, #e9ecef); border-radius: 8px;">
    <h2 style="color: #2c3e50; margin: 0 0 25px 0; font-size: 24px; font-weight: 600;">Cardiac Syntax Analysis</h2>
    <div style="display: grid; gap: 15px;">
        {cards_html}
    </div>
</div>"""

    @staticmethod
    def generate_detection_results(results: Dict) -> str:
        """Generate HTML output with cardiac syntax detection results for new format.

        Args:
            results: Dictionary containing:
                    'data': dict with 'predictions', 'diagnosis', 'modelRecommendations'
                    'message': str with status message
                    'success': bool indicating success

        Returns:
            str: HTML formatted string containing the classification result
        """
        
        # Validate input
        if not HTMLParser._validate_input(results):
            logging.error("Invalid input data for HTML generation.")
            return "Error: Invalid input data."

        # Extract data
        predictions = results.get('probability', {})
        recommendations = results.get('recommendations', {})

        # Use StringIO for the entire HTML generation
        buffer = StringIO()
        
        # Write HTML header
        buffer.write("""<!DOCTYPE html>
        <html>
        <head>
            <title>Cardiac Syntax Detection Report</title>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <style>
                """)
        buffer.write(HTMLParser._get_css_styles())
        buffer.write("""
            </style>
        </head>
        <body>
            <div class="detection-results">
                <div class="header">
                    <h1>Cardiac Syntax Analysis Results Report</h1>
                    <p class="subtitle">AI-Powered Cardiac SYNTAX Score Estimation — Thresholds: No Disease ≤2.23 | Mild 2.23–20.92 | Moderate 20.92–28.25 | Severe >28.25</p>
                </div>
                
                """)
                       
        # Write diagnosis summary section
        buffer.write(HTMLParser._generate_diagnosis_section(results.get('probability', {})))
        
        # Write recommendations section
        buffer.write(HTMLParser._generate_recommendations_section(recommendations))
        
        # Write footer
        buffer.write(f"""
                <!-- Important Note -->
                <div style="background-color: #fff3cd; padding: 15px; border-radius: 6px; border-left: 4px solid #ffc107; margin: 15px 0;">
                    <strong>Important:</strong> This AI analysis is for screening purposes only and should be interpreted by a qualified cardiologist. Clinical correlation with patient symptoms, risk factors, and additional imaging may be necessary for definitive diagnosis and treatment planning.
                </div>

                <div class="timestamp" style="text-align: center; margin: 30px 0;">
                    Report generated on {datetime.now().strftime("%Y-%m-%d %H:%M:%S UTC")}
                </div>
            </div>
        </body>
        </html>
        """)
        
        return buffer.getvalue()

# Example usage with test data
if __name__ == "__main__":
    import os
    import tempfile
    import webbrowser

    # Create test data for coronary stenosis
    test_results = {
        "probability": {
            "leftmain_stenosis_binary": 0.85,
            "lad_stenosis_binary": 0.65,
            "lcx_stenosis_binary": 0.25,
            "prox_rca_stenosis_binary": 0.15,
            "lad_stenosis": 75.2,
            "lcx_stenosis": 35.1,
            "prox_rca_stenosis": 20.3,
        },
        "diagnosis": '{"leftmain_stenosis_binary": "blocked", "lad_stenosis_binary": "blocked", "lcx_stenosis_binary": "normal", "prox_rca_stenosis_binary": "normal"}',
        "recommendations": {
            "en": "Significant stenosis detected in left main and LAD arteries. Urgent cardiology consultation recommended for revascularization evaluation.",
            "fr": "Sténose significative détectée dans le tronc commun gauche et l'artère IVA. Consultation cardiologique urgente recommandée pour évaluation de revascularisation.",
        },
    }

    # Generate HTML
    html_content = HTMLParser.generate_detection_results(test_results)

    # Save and display in browser
    with tempfile.NamedTemporaryFile(
        delete=False, suffix=".html", mode="w", encoding="utf-8"
    ) as temp_file:
        with open(temp_file.name, "w", encoding="utf-8") as f:
            f.write(html_content)
        webbrowser.open("file://" + os.path.realpath(temp_file.name))
