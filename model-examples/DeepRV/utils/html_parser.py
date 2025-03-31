class HTMLParser:
    @staticmethod
    def generate_detection_results(results):
        """Generate HTML output with detection results.
        
        Args:
            patient_age: Age of the patient
            results: Dictionary containing device detection results with keys:
                    'device_info', 'confidence', 'image_quality', 'images'
        
        Returns:
            str: HTML formatted string containing the detection results
        """
        rv = f"Normal Right Ventricle" if results['probability'] > 0.5 else "Abnormal Right Ventricle"
        html_content = f"""
        <!DOCTYPE html>
        <html>
        <head>
            <title>Device Detection Report</title>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <style>
                :root {{
                    --primary-color: rgb(0, 255, 0);
                    --secondary-color: rgb(0, 200, 0);
                    --background-color: #000000;
                    --text-color: #ffffff;
                    --border-color: #333333;
                }}

                body {{
                    font-family: Arial, sans-serif;
                    margin: 0;
                    padding: 15px;
                    background-color: var(--background-color);
                    color: var(--text-color);
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
                    margin-bottom: 20px;
                    padding-bottom: 15px;
                    border-bottom: 2px solid var(--border-color);
                }}

                .header h1 {{
                    color: var(--primary-color);
                    margin: 0;
                    font-size: 28px;
                    font-family: 'Arial Black', Arial, sans-serif;
                }}

                .value-label {{
                    font-weight: bold;
                    color: var(--primary-color);
                }}
            </style>
        </head>
        <body>
            <div class="detection-results">
                <div class="header">
                    <h1>Right Ventricle Detection Report</h1>
                    <p><span class="value-label">Right Ventricle:</span> {rv}</p>
                </div>
        """
                
        html_content += """
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

    # Create test data
    test_results = {
        'rv': 0.95
    }

    # Generate HTML
    html_content = HTMLParser.generate_detection_results(
        results=test_results
    )

    # Save and display in browser
    temp_file = tempfile.NamedTemporaryFile(delete=False, suffix='.html')
    try:
        with open(temp_file.name, 'w', encoding='utf-8') as f:
            f.write(html_content)
        webbrowser.open('file://' + os.path.realpath(temp_file.name))
    finally:
        temp_file.close()