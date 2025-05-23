import json

class HTMLParser:
    @staticmethod
    def generate_detection_results(results):
        """Generate HTML output with coronary stenosis detection results.
        
        Args:
            results: Dictionary containing:
                    'probability': dict with prediction values for each artery
                    'diagnosis': JSON string with diagnosis for each head
                    'recommendations': dict with 'en' and 'fr' recommendations
        
        Returns:
            str: HTML formatted string containing the classification result
        """
        probability_dict = results['probability']
        diagnosis_json = results.get('diagnosis', '{}')
        recommendations = results.get('recommendations', {})
        
        # Parse diagnosis JSON string
        try:
            diagnosis_dict = json.loads(diagnosis_json) if isinstance(diagnosis_json, str) else diagnosis_json
        except:
            diagnosis_dict = {}
        
        # Separate binary and regression predictions
        binary_predictions = {k: v for k, v in probability_dict.items() if '_binary' in k}
        regression_predictions = {k: v for k, v in probability_dict.items() if '_binary' not in k}
        
        # Find blocked arteries (above threshold)
        blocked_arteries = []
        normal_arteries = []
        
        for head, prob in binary_predictions.items():
            # Assume threshold of 0.5 for binary classification
            if prob > 0.5:
                blocked_arteries.append((head, prob))
            else:
                normal_arteries.append((head, prob))
        
        # Determine overall status
        has_stenosis = len(blocked_arteries) > 0
        status_color = "#ff6b6b" if has_stenosis else "#4ecdc4"
        overall_status = "Coronary Stenosis Detected" if has_stenosis else "No Significant Stenosis"
        
        # Artery display names
        artery_names = {
            "leftmain_stenosis_binary": "Left Main",
            "lad_stenosis_binary": "LAD (Left Anterior Descending)",
            "mid_lad_stenosis_binary": "Mid LAD",
            "dist_lad_stenosis_binary": "Distal LAD",
            "diagonal_stenosis_binary": "Diagonal Branch",
            "D2_stenosis_binary": "D2 Branch",
            "lcx_stenosis_binary": "LCX (Left Circumflex)",
            "dist_lcx_stenosis_binary": "Distal LCX",
            "om1_stenosis_binary": "OM1 (Obtuse Marginal 1)",
            "om2_stenosis_binary": "OM2 (Obtuse Marginal 2)",
            "bx_stenosis_binary": "Branch Vessel",
            "prox_rca_stenosis_binary": "Proximal RCA",
            "mid_rca_stenosis_binary": "Mid RCA",
            "dist_rca_stenosis_binary": "Distal RCA",
            "pda_stenosis_binary": "PDA (Posterior Descending)",
            "posterolateral_stenosis_binary": "Posterolateral Branch"
        }
        
        html_content = f"""
        <!DOCTYPE html>
        <html>
        <head>
            <title>Coronary Stenosis Detection Report</title>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <style>
                :root {{
                    --primary-color: #3498db;
                    --secondary-color: #2980b9;
                    --background-color: #f8f9fa;
                    --text-color: #2c3e50;
                    --border-color: #e9ecef;
                    --status-color: {status_color};
                    --warning-color: #e74c3c;
                    --normal-color: #27ae60;
                    --card-bg: #ffffff;
                }}

                body {{
                    font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
                    margin: 0;
                    padding: 20px;
                    background-color: var(--background-color);
                    color: var(--text-color);
                    line-height: 1.6;
                }}

                .detection-results {{
                    max-width: 1200px;
                    margin: 0 auto;
                    background-color: var(--card-bg);
                    border-radius: 12px;
                    box-shadow: 0 4px 6px rgba(0,0,0,0.1);
                    padding: 30px;
                    border: 1px solid var(--border-color);
                }}

                .header {{
                    text-align: center;
                    margin-bottom: 40px;
                    padding-bottom: 30px;
                    border-bottom: 3px solid var(--primary-color);
                }}

                .header h1 {{
                    color: var(--primary-color);
                    margin: 0 0 10px 0;
                    font-size: 32px;
                    font-weight: 700;
                }}

                .header .subtitle {{
                    color: var(--secondary-color);
                    font-size: 16px;
                    margin: 0;
                }}

                .section {{
                    background-color: var(--card-bg);
                    border-radius: 8px;
                    padding: 25px;
                    margin: 25px 0;
                    border: 1px solid var(--border-color);
                    box-shadow: 0 2px 4px rgba(0,0,0,0.05);
                }}

                .section h2 {{
                    color: var(--primary-color);
                    margin: 0 0 20px 0;
                    font-size: 24px;
                    font-weight: 600;
                }}

                .overall-status {{
                    text-align: center;
                    padding: 30px;
                    background: linear-gradient(135deg, {status_color}15, {status_color}05);
                    border-radius: 12px;
                    border: 2px solid var(--status-color);
                    margin-bottom: 30px;
                }}

                .status-text {{
                    font-size: 28px;
                    font-weight: bold;
                    color: var(--status-color);
                    margin: 0;
                }}

                .status-summary {{
                    font-size: 16px;
                    color: var(--text-color);
                    margin-top: 10px;
                }}

                .arteries-grid {{
                    display: grid;
                    grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
                    gap: 20px;
                    margin: 25px 0;
                }}

                .artery-card {{
                    background: var(--card-bg);
                    padding: 20px;
                    border-radius: 8px;
                    border-left: 4px solid;
                    box-shadow: 0 2px 4px rgba(0,0,0,0.1);
                }}

                .artery-card.blocked {{
                    border-left-color: var(--warning-color);
                    background: linear-gradient(135deg, #e74c3c15, #e74c3c05);
                }}

                .artery-card.normal {{
                    border-left-color: var(--normal-color);
                    background: linear-gradient(135deg, #27ae6015, #27ae6005);
                }}

                .artery-name {{
                    font-weight: bold;
                    color: var(--text-color);
                    font-size: 16px;
                    margin-bottom: 10px;
                }}

                .artery-probability {{
                    font-size: 14px;
                    color: var(--secondary-color);
                    margin-bottom: 8px;
                }}

                .artery-status {{
                    font-weight: bold;
                    font-size: 14px;
                }}

                .artery-status.blocked {{
                    color: var(--warning-color);
                }}

                .artery-status.normal {{
                    color: var(--normal-color);
                }}

                .probability-bar {{
                    width: 100%;
                    height: 8px;
                    background-color: #e9ecef;
                    border-radius: 4px;
                    overflow: hidden;
                    margin: 8px 0;
                }}

                .probability-fill {{
                    height: 100%;
                    border-radius: 4px;
                    transition: width 0.3s ease;
                }}

                .blocked .probability-fill {{
                    background: linear-gradient(90deg, #f39c12, #e74c3c);
                }}

                .normal .probability-fill {{
                    background: linear-gradient(90deg, #f1c40f, #27ae60);
                }}

                .summary-stats {{
                    display: grid;
                    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
                    gap: 20px;
                    margin: 25px 0;
                }}

                .stat-card {{
                    background: var(--card-bg);
                    padding: 20px;
                    border-radius: 8px;
                    text-align: center;
                    border: 1px solid var(--border-color);
                    box-shadow: 0 2px 4px rgba(0,0,0,0.05);
                }}

                .stat-number {{
                    font-size: 32px;
                    font-weight: bold;
                    color: var(--primary-color);
                    margin-bottom: 5px;
                }}

                .stat-label {{
                    font-size: 14px;
                    color: var(--secondary-color);
                    text-transform: uppercase;
                    font-weight: 500;
                }}

                .recommendations {{
                    background: linear-gradient(135deg, var(--primary-color)15, var(--primary-color)05);
                    border-left: 4px solid var(--primary-color);
                    padding: 20px 25px;
                    margin: 20px 0;
                    border-radius: 8px;
                }}

                .recommendation-text {{
                    margin: 15px 0;
                    font-size: 16px;
                    line-height: 1.6;
                }}

                .language-label {{
                    font-weight: bold;
                    color: var(--primary-color);
                    text-transform: uppercase;
                    font-size: 13px;
                    margin-bottom: 8px;
                    display: block;
                }}

                .timestamp {{
                    text-align: center;
                    color: var(--secondary-color);
                    font-size: 12px;
                    margin-top: 40px;
                    padding-top: 20px;
                    border-top: 1px solid var(--border-color);
                }}

                .highlight {{
                    background-color: #fff3cd;
                    padding: 15px;
                    border-radius: 6px;
                    border-left: 4px solid #ffc107;
                    margin: 15px 0;
                }}
            </style>
        </head>
        <body>
            <div class="detection-results">
                <div class="header">
                    <h1>Coronary Stenosis Detection Report</h1>
                    <p class="subtitle">AI-Powered Cardiac Analysis</p>
                </div>

                <div class="overall-status">
                    <p class="status-text">{overall_status}</p>
                    <p class="status-summary">
                        {len(blocked_arteries)} vessel(s) with significant stenosis detected out of {len(binary_predictions)} analyzed
                    </p>
                </div>

                <div class="section">
                    <h2>Summary Statistics</h2>
                    <div class="summary-stats">
                        <div class="stat-card">
                            <div class="stat-number">{len(blocked_arteries)}</div>
                            <div class="stat-label">Blocked Vessels</div>
                        </div>
                        <div class="stat-card">
                            <div class="stat-number">{len(normal_arteries)}</div>
                            <div class="stat-label">Normal Vessels</div>
                        </div>
                        <div class="stat-card">
                            <div class="stat-number">{len(binary_predictions)}</div>
                            <div class="stat-label">Total Analyzed</div>
                        </div>
                        <div class="stat-card">
                            <div class="stat-number">{(len(blocked_arteries)/len(binary_predictions)*100):.1f}%</div>
                            <div class="stat-label">Stenosis Rate</div>
                        </div>
                    </div>
                </div>
        """

        # Add detailed vessel analysis
        if binary_predictions:
            html_content += """
                <div class="section">
                    <h2>Detailed Vessel Analysis</h2>
            """
            
            # Show blocked arteries first
            if blocked_arteries:
                html_content += "<h3 style='color: var(--warning-color); margin-bottom: 15px;'>⚠️ Vessels with Significant Stenosis</h3>"
                html_content += '<div class="arteries-grid">'
                
                for head, prob in sorted(blocked_arteries, key=lambda x: x[1], reverse=True):
                    artery_name = artery_names.get(head, head.replace('_stenosis_binary', '').replace('_', ' ').title())
                    diagnosis_text = diagnosis_dict.get(head, 'blocked')
                    
                    html_content += f"""
                        <div class="artery-card blocked">
                            <div class="artery-name">{artery_name}</div>
                            <div class="artery-probability">Probability: {prob:.3f}</div>
                            <div class="probability-bar">
                                <div class="probability-fill" style="width: {prob * 100}%;"></div>
                            </div>
                            <div class="artery-status blocked">Status: {diagnosis_text.upper()}</div>
                        </div>
                    """
                
                html_content += '</div>'
            
            # Show normal arteries
            if normal_arteries:
                html_content += "<h3 style='color: var(--normal-color); margin-top: 30px; margin-bottom: 15px;'>✅ Normal Vessels</h3>"
                html_content += '<div class="arteries-grid">'
                
                for head, prob in sorted(normal_arteries, key=lambda x: x[1]):
                    artery_name = artery_names.get(head, head.replace('_stenosis_binary', '').replace('_', ' ').title())
                    diagnosis_text = diagnosis_dict.get(head, 'normal')
                    
                    html_content += f"""
                        <div class="artery-card normal">
                            <div class="artery-name">{artery_name}</div>
                            <div class="artery-probability">Probability: {prob:.3f}</div>
                            <div class="probability-bar">
                                <div class="probability-fill" style="width: {prob * 100}%;"></div>
                            </div>
                            <div class="artery-status normal">Status: {diagnosis_text.upper()}</div>
                        </div>
                    """
                
                html_content += '</div>'
            
            html_content += "</div>"

        # Add regression results if available
        if regression_predictions:
            html_content += """
                <div class="section">
                    <h2>Quantitative Stenosis Assessment</h2>
                    <div class="arteries-grid">
            """
            
            for head, value in regression_predictions.items():
                artery_name = artery_names.get(head + '_binary', head.replace('_stenosis', '').replace('_', ' ').title())
                
                html_content += f"""
                    <div class="artery-card {'blocked' if value > 70 else 'normal'}">
                        <div class="artery-name">{artery_name}</div>
                        <div class="artery-probability">Stenosis Percentage: {value:.1f}%</div>
                        <div class="probability-bar">
                            <div class="probability-fill" style="width: {min(value, 100)}%;"></div>
                        </div>
                        <div class="artery-status {'blocked' if value > 70 else 'normal'}">
                            {'Severe' if value > 70 else 'Moderate' if value > 50 else 'Mild' if value > 30 else 'Minimal'}
                        </div>
                    </div>
                """
            
            html_content += """
                    </div>
                </div>
            """

        # Add recommendations section if available
        if recommendations:
            html_content += """
                <div class="section">
                    <h2>Clinical Recommendations</h2>
            """
            
            if 'en' in recommendations:
                html_content += f"""
                    <div class="recommendations">
                        <span class="language-label">English</span>
                        <div class="recommendation-text">{recommendations['en']}</div>
                    </div>
                """
            
            if 'fr' in recommendations:
                html_content += f"""
                    <div class="recommendations">
                        <span class="language-label">Français</span>
                        <div class="recommendation-text">{recommendations['fr']}</div>
                    </div>
                """
            
            html_content += "</div>"

        # Add important note
        html_content += """
            <div class="highlight">
                <strong>Important:</strong> This AI analysis is for screening purposes only and should be interpreted by a qualified cardiologist. 
                Clinical correlation with patient symptoms, risk factors, and additional imaging may be necessary for definitive diagnosis and treatment planning.
            </div>
        """

        # Add timestamp and close
        html_content += f"""
                <div class="timestamp">
                    Report generated on {__import__('datetime').datetime.now().strftime('%Y-%m-%d %H:%M:%S UTC')}
                </div>
            </div>
        </body>
        </html>
        """
        return html_content

# Example usage with test data
if __name__ == "__main__":
    import webbrowser
    import tempfile
    import os

    # Create test data for coronary stenosis
    test_results = {
        'probability': {
            'leftmain_stenosis_binary': 0.85,
            'lad_stenosis_binary': 0.65,
            'lcx_stenosis_binary': 0.25,
            'prox_rca_stenosis_binary': 0.15,
            'lad_stenosis': 75.2,
            'lcx_stenosis': 35.1,
            'prox_rca_stenosis': 20.3
        },
        'diagnosis': '{"leftmain_stenosis_binary": "blocked", "lad_stenosis_binary": "blocked", "lcx_stenosis_binary": "normal", "prox_rca_stenosis_binary": "normal"}',
        'recommendations': {
            'en': 'Significant stenosis detected in left main and LAD arteries. Urgent cardiology consultation recommended for revascularization evaluation.',
            'fr': 'Sténose significative détectée dans le tronc commun gauche et l\'artère IVA. Consultation cardiologique urgente recommandée pour évaluation de revascularisation.'
        }
    }

    # Generate HTML
    html_content = HTMLParser.generate_detection_results(test_results)

    # Save and display in browser
    temp_file = tempfile.NamedTemporaryFile(delete=False, suffix='.html')
    try:
        with open(temp_file.name, 'w', encoding='utf-8') as f:
            f.write(html_content)
        webbrowser.open('file://' + os.path.realpath(temp_file.name))
    finally:
        temp_file.close()