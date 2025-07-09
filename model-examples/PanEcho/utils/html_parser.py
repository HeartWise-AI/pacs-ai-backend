import json

class HTMLParser:
    @staticmethod
    def generate_detection_results(results):
        """Generate HTML output with echocardiographic analysis results.
        
        Args:
            results: Dictionary containing:
                    'probability': dict with prediction values for each parameter
                    'diagnosis': JSON string with diagnosis for each head
                    'recommendations': dict with 'en' and 'fr' recommendations
        
        Returns:
            str: HTML formatted string containing the analysis results
        """
        probability_dict = results['probability']
        diagnosis_json = results.get('diagnosis', '{}')
        print('diagnosis_json: ', diagnosis_json)
        recommendations = results.get('recommendations', {})
        
        # Parse diagnosis JSON string
        try:
            diagnosis_dict = json.loads(diagnosis_json) if isinstance(diagnosis_json, str) else diagnosis_json
        except:
            diagnosis_dict = {}
        
        # Categorize findings
        abnormal_findings = []
        normal_findings = []
        urgent_findings = []
        
        # Define categories for better organization
        categories = {
            'LV_Function': {
                'title': 'Left Ventricular Function',
                'icon': '🫀',
                'findings': {}
            },
            'RV_Function': {
                'title': 'Right Ventricular Function', 
                'icon': '💓',
                'findings': {}
            },
            'Valves': {
                'title': 'Valvular Assessment',
                'icon': '🚪',
                'findings': {}
            },
            'Measurements': {
                'title': 'Cardiac Measurements',
                'icon': '📏',
                'findings': {}
            },
            'Other': {
                'title': 'Additional Findings',
                'icon': '🔍',
                'findings': {}
            }
        }
        
        # Categorize each finding
        for key, value in diagnosis_dict.items():
            category = 'Other'  # Default category
            
            # Categorize based on key patterns
            if any(lv_key in key.lower() for lv_key in ['lv', 'ef', 'gls', 'lvedv', 'lvesv', 'lvsv']):
                category = 'LV_Function'
            elif any(rv_key in key.lower() for rv_key in ['rv', 'tapse']):
                category = 'RV_Function'
            elif any(valve_key in key.lower() for valve_key in ['av', 'mv', 'tv', 'stenosis', 'regurg']):
                category = 'Valves'
            elif any(meas_key in key.lower() for meas_key in ['ivs', 'lvpw', 'lvid', 'lvot', 'la', 'ra', 'ao']):
                category = 'Measurements'
            elif key.lower() in ['pericardial-effusion']:
                category = 'Other'
            
            # Determine if finding is abnormal
            is_abnormal = False
            is_urgent = False

            if isinstance(value, str):
                value_lower = value.lower()
                
                # First check if it's explicitly normal/absent
                if any(normal_indicator in value_lower for normal_indicator in ['normal', 'absent', 'none']):
                    is_abnormal = False
                else:
                    # Check for abnormal patterns
                    abnormal_patterns = [
                        'moderately', 'severely', 'mild', 'moderate', 'severe',
                        'increased', 'decreased', 'dilated', 'dysfunction', 'present'
                    ]
                    
                    # Special handling for measurement values with units
                    if any(unit in value_lower for unit in ['percentage', 'cm^3', 'cm/s', 'mmhg', 'ratio', 'm/s']):
                        # These are just measurements, not abnormal unless they contain abnormal descriptors
                        if any(pattern in value_lower for pattern in ['moderately', 'severely', 'mild', 'moderate', 'severe']):
                            is_abnormal = True
                    else:
                        # Check for abnormal descriptors
                        if any(pattern in value_lower for pattern in abnormal_patterns):
                            is_abnormal = True
                    
                    # Check for urgent findings
                    urgent_patterns = ['severely', 'severe']
                    if any(pattern in value_lower for pattern in urgent_patterns):
                        is_urgent = True
            else:
                # For non-string values, consider them normal measurements
                is_abnormal = False
            
            # Add to appropriate category
            categories[category]['findings'][key] = {
                'value': value,
                'is_abnormal': is_abnormal,
                'is_urgent': is_urgent,
                'probability': probability_dict.get(key, 0)
            }
            
            # Track for summary
            if is_urgent:
                urgent_findings.append(key)
            elif is_abnormal:
                abnormal_findings.append(key)
            else:
                normal_findings.append(key)
        print(f'urgent_findings: {len(urgent_findings)}')
        print(f'abnormal_findings: {len(abnormal_findings)}')
        print(f'normal_findings: {len(normal_findings)}')
        
        # Determine overall status
        total_abnormal = len(abnormal_findings) + len(urgent_findings)
        has_abnormalities = total_abnormal > 0
        has_urgent = len(urgent_findings) > 0
        
        if has_urgent:
            status_color = "#e74c3c"
            overall_status = "Urgent Abnormalities Detected"
        elif has_abnormalities:
            status_color = "#f39c12"
            overall_status = "Abnormalities Detected"
        else:
            status_color = "#27ae60"
            overall_status = "Normal Echocardiogram"
        
        html_content = f"""
        <!DOCTYPE html>
        <html>
        <head>
            <title>Echocardiographic Analysis Report</title>
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
                    --caution-color: #f39c12;
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

                .findings-grid {{
                    display: grid;
                    grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
                    gap: 20px;
                    margin: 25px 0;
                }}

                .finding-card {{
                    background: var(--card-bg);
                    padding: 20px;
                    border-radius: 8px;
                    border-left: 4px solid;
                    box-shadow: 0 2px 4px rgba(0,0,0,0.1);
                }}

                .finding-card.urgent {{
                    border-left-color: var(--warning-color);
                    background: linear-gradient(135deg, #e74c3c15, #e74c3c05);
                }}

                .finding-card.abnormal {{
                    border-left-color: var(--caution-color);
                    background: linear-gradient(135deg, #f39c1215, #f39c1205);
                }}

                .finding-card.normal {{
                    border-left-color: var(--normal-color);
                    background: linear-gradient(135deg, #27ae6015, #27ae6005);
                }}

                .finding-name {{
                    font-weight: bold;
                    color: var(--text-color);
                    font-size: 16px;
                    margin-bottom: 10px;
                }}

                .finding-value {{
                    font-size: 18px;
                    font-weight: 600;
                    margin-bottom: 8px;
                }}

                .finding-status {{
                    font-weight: bold;
                    font-size: 14px;
                }}

                .finding-status.urgent {{
                    color: var(--warning-color);
                }}

                .finding-status.abnormal {{
                    color: var(--caution-color);
                }}

                .finding-status.normal {{
                    color: var(--normal-color);
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

                .category-title {{
                    color: var(--primary-color);
                    font-size: 18px;
                    font-weight: 600;
                    margin: 20px 0 15px 0;
                    display: flex;
                    align-items: center;
                    gap: 10px;
                }}
            </style>
        </head>
        <body>
            <div class="detection-results">
                <div class="header">
                    <h1>Echocardiographic Analysis Report</h1>
                    <p class="subtitle">AI-Powered Cardiac Assessment</p>
                </div>

                <div class="overall-status">
                    <p class="status-text">{overall_status}</p>
                    <p class="status-summary">
                        {total_abnormal} abnormal finding(s) detected out of {len(diagnosis_dict)} analyzed parameters
                    </p>
                </div>

                <div class="section">
                    <h2>Summary Statistics</h2>
                    <div class="summary-stats">
                        <div class="stat-card">
                            <div class="stat-number">{len(urgent_findings)}</div>
                            <div class="stat-label">Urgent Findings</div>
                        </div>
                        <div class="stat-card">
                            <div class="stat-number">{len(abnormal_findings)}</div>
                            <div class="stat-label">Abnormal Findings</div>
                        </div>
                        <div class="stat-card">
                            <div class="stat-number">{len(normal_findings)}</div>
                            <div class="stat-label">Normal Parameters</div>
                        </div>
                        <div class="stat-card">
                            <div class="stat-number">{len(diagnosis_dict)}</div>
                            <div class="stat-label">Total Analyzed</div>
                        </div>
                    </div>
                </div>
        """

        # Add detailed findings by category
        for category_key, category_data in categories.items():
            if category_data['findings']:
                html_content += f"""
                <div class="section">
                        <h2>{category_data['icon']} {category_data['title']}</h2>
                        <div class="findings-grid">
                """
                
                # Sort findings: urgent first, then abnormal, then normal
                sorted_findings = sorted(
                    category_data['findings'].items(),
                    key=lambda x: (
                        0 if x[1]['is_urgent'] else 1 if x[1]['is_abnormal'] else 2,
                        x[0]
                    )
                )
                print(f'sorted_findings: {sorted_findings}')
                for finding_key, finding_data in sorted_findings:
                    print(f'finding_key: {finding_key}, finding_data: {finding_data}')
                    
                    # Determine card class
                    if finding_data['is_urgent']:
                        card_class = "urgent"
                        status_text = "URGENT"
                        status_class = "urgent"
                    elif finding_data['is_abnormal']:
                        card_class = "abnormal"
                        status_text = "ABNORMAL"
                        status_class = "abnormal"
                    else:
                        card_class = "normal"
                        status_text = "NORMAL"
                        status_class = "normal"
                        
                    html_content += f"""
                    <div class="finding-card {card_class}">
                        <div class="finding-name">{finding_key}</div>
                        <div class="finding-value">{finding_data['value']}</div>
                        <div class="finding-status {status_class}">Status: {status_text}</div>
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
                Clinical correlation with patient symptoms, history, and physical examination is essential for definitive diagnosis and treatment planning.
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

    # Create test data for echocardiographic analysis
    test_results = {
        'probability': {
            'leftmain_stenosis_binary': 0.85,
            'lad_stenosis_binary': 0.65,
            'lcx_stenosis_binary': 0.25,
            'prox_rca_stenosis_binary': 0.15,
            'lad_stenosis': 75.2,
            'lcx_stenosis': 35.1,
            'prox_rca_stenosis': 20.3,
            'lv_ef': 65,
            'rv_tapse': 25,
            'av_stenosis': 'moderate',
            'mv_stenosis': 'severe',
            'ivs': 12,
            'lvpw': 45,
            'lvid': 5.5,
            'lvot': 2.2,
            'la': 3.0,
            'ra': 2.5,
            'ao': 1.0,
            'pericardial-effusion': 'present'
        },
        'diagnosis': '{"leftmain_stenosis_binary": "blocked", "lad_stenosis_binary": "blocked", "lcx_stenosis_binary": "normal", "prox_rca_stenosis_binary": "normal", "lv_ef": "increased", "rv_tapse": "increased", "av_stenosis": "severe", "mv_stenosis": "severe", "ivs": "increased", "lvpw": "increased", "lvid": "increased", "lvot": "increased", "la": "increased", "ra": "increased", "ao": "increased", "pericardial-effusion": "present"}',
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