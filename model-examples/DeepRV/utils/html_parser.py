class HTMLParser:
    @staticmethod
    def generate_detection_results(results):
        """Generate HTML output with detection results.

        Args:
            results: Dictionary containing with keys:
                    'probability': float between 0 and 1
                    'diagnosis': str (optional)
                    'confidence': str (optional)
                    'recommendations': dict (optional)

        Returns:
            str: HTML formatted string containing the classification result
        """
        probability = results["probability"]
        diagnosis = results.get(
            "diagnosis",
            "Reduced Right Ventricular Function"
            if probability > 0.1
            else "Normal Right Ventricular Function",
        )
        confidence = results.get(
            "confidence",
            "high"
            if abs(probability - 0.1) > 0.3
            else "intermediate"
            if abs(probability - 0.1) > 0.15
            else "low",
        )
        recommendations = results.get("recommendations", {})

        # Determine status for styling
        is_reduced = probability > 0.1
        status_color = "#ff6b6b" if is_reduced else "#4ecdc4"

        html_content = f"""
        <!DOCTYPE html>
        <html>
        <head>
            <title>Right Ventricle Detection Report</title>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <style>
                :root {{
                    --primary-color: rgb(0, 255, 0);
                    --secondary-color: rgb(0, 200, 0);
                    --background-color: #000000;
                    --text-color: #ffffff;
                    --border-color: #333333;
                    --status-color: {status_color};
                    --warning-color: #ff6b6b;
                    --normal-color: #4ecdc4;
                }}

                body {{
                    font-family: Arial, sans-serif;
                    margin: 0;
                    padding: 15px;
                    background-color: var(--background-color);
                    color: var(--text-color);
                    line-height: 1.6;
                }}

                .detection-results {{
                    max-width: 900px;
                    margin: 0 auto;
                    background-color: #111111;
                    border-radius: 8px;
                    box-shadow: 0 2px 4px rgba(0,255,0,0.1);
                    padding: 20px;
                    border: 1px solid var(--border-color);
                }}

                .header {{
                    text-align: center;
                    margin-bottom: 30px;
                    padding-bottom: 20px;
                    border-bottom: 2px solid var(--border-color);
                }}

                .header h1 {{
                    color: var(--primary-color);
                    margin: 0 0 10px 0;
                    font-size: 28px;
                    font-family: 'Arial Black', Arial, sans-serif;
                }}

                .header .subtitle {{
                    color: var(--secondary-color);
                    font-size: 14px;
                    margin: 0;
                }}

                .section {{
                    background-color: #1a1a1a;
                    border-radius: 8px;
                    padding: 20px;
                    margin: 20px 0;
                    border: 1px solid var(--border-color);
                }}

                .section h2 {{
                    color: var(--primary-color);
                    margin: 0 0 15px 0;
                    font-size: 20px;
                    font-family: 'Arial Black', Arial, sans-serif;
                }}

                .diagnosis-result {{
                    text-align: center;
                    padding: 20px;
                    background-color: #222222;
                    border-radius: 8px;
                    border: 2px solid var(--status-color);
                }}

                .diagnosis-text {{
                    font-size: 24px;
                    font-weight: bold;
                    color: var(--status-color);
                    margin: 0;
                }}

                .metrics-grid {{
                    display: grid;
                    grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
                    gap: 15px;
                    margin: 20px 0;
                }}

                .metric-item {{
                    background: #222222;
                    padding: 15px;
                    border-radius: 6px;
                    border: 1px solid var(--border-color);
                }}

                .metric-label {{
                    font-weight: bold;
                    color: var(--primary-color);
                    display: block;
                    margin-bottom: 5px;
                }}

                .metric-value {{
                    font-size: 18px;
                    color: var(--text-color);
                }}

                .probability-bar {{
                    width: 100%;
                    height: 20px;
                    background-color: #333;
                    border-radius: 10px;
                    overflow: hidden;
                    margin: 10px 0;
                }}

                .probability-fill {{
                    height: 100%;
                    background: linear-gradient(90deg, var(--normal-color) 0%, var(--warning-color) 100%);
                    width: {probability * 100}%;
                    transition: width 0.3s ease;
                }}

                .confidence-badge {{
                    display: inline-block;
                    padding: 4px 12px;
                    border-radius: 12px;
                    font-size: 12px;
                    font-weight: bold;
                    text-transform: uppercase;
                    background-color: {"#28a745" if confidence == "high" else "#ffc107" if confidence == "intermediate" else "#dc3545"};
                    color: {"#ffffff" if confidence != "intermediate" else "#000000"};
                }}

                .recommendations {{
                    background-color: #1a1a1a;
                    border-left: 4px solid var(--primary-color);
                    padding: 15px 20px;
                    margin: 15px 0;
                }}

                .recommendation-text {{
                    margin: 10px 0;
                    font-size: 16px;
                    line-height: 1.5;
                }}

                .language-label {{
                    font-weight: bold;
                    color: var(--primary-color);
                    text-transform: uppercase;
                    font-size: 12px;
                    margin-bottom: 5px;
                }}

                .value-label {{
                    font-weight: bold;
                    color: var(--primary-color);
                }}

                .timestamp {{
                    text-align: center;
                    color: var(--secondary-color);
                    font-size: 12px;
                    margin-top: 30px;
                    padding-top: 15px;
                    border-top: 1px solid var(--border-color);
                }}
            </style>
        </head>
        <body>
            <div class="detection-results">
                <div class="header">
                    <h1>Right Ventricle Detection Report</h1>
                    <p class="subtitle">Deep Learning Analysis Results</p>
                </div>

                <div class="section">
                    <h2>Primary Diagnosis</h2>
                    <div class="diagnosis-result">
                        <p class="diagnosis-text">{diagnosis}</p>
                    </div>
                </div>

                <div class="section">
                    <h2>Analysis Metrics</h2>
                    <div class="metrics-grid">
                        <div class="metric-item">
                            <span class="metric-label">Probability Score</span>
                            <div class="metric-value">{probability:.3f}</div>
                            <div class="probability-bar">
                                <div class="probability-fill"></div>
                            </div>
                        </div>
                        <div class="metric-item">
                            <span class="metric-label">Confidence Level</span>
                            <div class="metric-value">
                                <span class="confidence-badge">{confidence}</span>
                            </div>
                        </div>
                        <div class="metric-item">
                            <span class="metric-label">Classification</span>
                            <div class="metric-value">{"Reduced Function" if is_reduced else "Normal Function"}</div>
                        </div>
                        <div class="metric-item">
                            <span class="metric-label">Model Threshold</span>
                            <div class="metric-value">0.100</div>
                        </div>
                    </div>
                </div>
        """

        # Add recommendations section if available
        if recommendations:
            html_content += """
                <div class="section">
                    <h2>Clinical Recommendations</h2>
            """

            if "en" in recommendations:
                html_content += f"""
                    <div class="recommendations">
                        <div class="language-label">English</div>
                        <div class="recommendation-text">{recommendations["en"]}</div>
                    </div>
                """

            if "fr" in recommendations:
                html_content += f"""
                    <div class="recommendations">
                        <div class="language-label">Français</div>
                        <div class="recommendation-text">{recommendations["fr"]}</div>
                    </div>
                """

            html_content += "</div>"

        # Add timestamp and close
        html_content += f"""
                <div class="timestamp">
                    Report generated on {__import__("datetime").datetime.now().strftime("%Y-%m-%d %H:%M:%S UTC")}
                </div>
            </div>
        </body>
        </html>
        """
        return html_content


# Example usage with test data
if __name__ == "__main__":
    import os
    import tempfile
    import webbrowser

    # Create test data
    test_results = {
        "probability": 0.75,
        "diagnosis": "Reduced Right Ventricular Function",
        "confidence": "high",
        "recommendations": {
            "en": "Reduced right ventricular function detected. Consider further cardiac evaluation and specialist consultation.",
            "fr": "Fonction réduite du ventricule droit détectée. Envisager une évaluation cardiaque plus approfondie et une consultation spécialisée.",
        },
    }

    # Generate HTML
    html_content = HTMLParser.generate_detection_results(results=test_results)

    # Save and display in browser
    with tempfile.NamedTemporaryFile(
        delete=False, suffix=".html", mode="w", encoding="utf-8"
    ) as temp_file:
        with open(temp_file.name, "w", encoding="utf-8") as f:
            f.write(html_content)
        webbrowser.open("file://" + os.path.realpath(temp_file.name))
