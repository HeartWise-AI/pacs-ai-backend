class HTMLParser:
    @staticmethod
    def generate_detection_results(patient_age, results=None):
        """Generate HTML output with detection results.

        Args:
            patient_age: Age of the patient
            results: Dictionary containing device detection results with keys:
                    'device_info', 'confidence', 'image_quality', 'images'

        Returns:
            str: HTML formatted string containing the detection results
        """
        age_text = f"{patient_age} years" if patient_age else "Not available"
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

                .device-info {{
                    background-color: #1a1a1a;
                    border-radius: 8px;
                    padding: 20px;
                    margin: 15px 0;
                    border: 1px solid var(--border-color);
                }}

                .device-info h4 {{
                    color: var(--primary-color);
                    margin: 0 0 15px 0;
                    font-size: 20px;
                }}

                .device-info img {{
                    display: block;
                    margin: 15px auto;
                    border: 2px solid var(--border-color);
                    border-radius: 4px;
                }}

                .device-info p {{
                    margin: 8px 0;
                    font-size: 16px;
                }}

                .value-label {{
                    font-weight: bold;
                    color: var(--primary-color);
                }}

                .no-devices {{
                    text-align: center;
                    padding: 20px;
                    color: var(--secondary-color);
                    font-size: 18px;
                }}

                .device-type {{
                    font-size: 14px;
                    color: var(--secondary-color);
                    margin-left: 8px;
                    display: inline;
                }}
            </style>
        </head>
        <body>
            <div class="detection-results">
                <div class="header">
                    <h1>Device Detection Report</h1>
                    <p><span class="value-label">Patient Age:</span> {age_text}</p>
                </div>
        """

        if results:
            html_content += "<div class='devices-container'>"
            for i in range(len(results["device_info"])):
                device = results["device_info"][i]
                html_content += f"""
                <div class='device-info'>
                    <h4>{device["type_full"]} <span class="device-type">({device["type_short"]})</span></h4>
                    <img src="data:image/png;base64,{results["images"][i]}" alt="Detected Device {i + 1}" style="max-width: 300px;"/>
                    <p><span class="value-label">Manufacturer:</span> {device["manufacturer"]}</p>
                    <p><span class="value-label">Confidence:</span> {results["confidence"][i]:.2%}</p>
                    <p><span class="value-label">Image Quality:</span> {results["image_quality"][i]:.1f}/4.0</p>
                </div>
                """
            html_content += "</div>"
        else:
            html_content += "<div class='no-devices'>No devices detected with confidence threshold of 0.95</div>"

        html_content += """
            </div>
        </body>
        </html>
        """
        return html_content


# Example usage with test data
if __name__ == "__main__":
    import base64
    import os
    import tempfile
    import webbrowser

    from PIL import Image

    # Create a dummy image for testing
    def create_dummy_image():
        # Create a 200x200 black image with a white rectangle
        img = Image.new("L", (200, 200), 0)  # Black background
        for x in range(50, 150):
            for y in range(50, 150):
                img.putpixel((x, y), 255)  # White rectangle

        # Convert to base64
        import io

        buffered = io.BytesIO()
        img.save(buffered, format="PNG")
        return base64.b64encode(buffered.getvalue()).decode("utf-8")

    # Create test data
    test_results = {
        "device_info": [
            {
                "raw_type": "BIO_ICD",
                "manufacturer": "Biotronik",
                "type_full": "Implantable cardioverter-defibrillator",
                "type_short": "ICD",
            },
            {
                "raw_type": "MED_PM",
                "manufacturer": "Medtronic",
                "type_full": "Pacemaker",
                "type_short": "Pacemaker",
            },
        ],
        "confidence": [0.98, 0.95],
        "image_quality": [3.8, 3.2],
        "images": [create_dummy_image() for _ in range(2)],
    }

    # Generate HTML
    html_content = HTMLParser.generate_detection_results(patient_age=65, results=test_results)

    # Save and display in browser
    with tempfile.NamedTemporaryFile(
        delete=False, suffix=".html", mode="w", encoding="utf-8"
    ) as temp_file:
        temp_file.write(html_content)
        temp_file_path = temp_file.name
    webbrowser.open("file://" + os.path.realpath(temp_file_path))

    # Also test the case with no devices detected
    html_content_no_devices = HTMLParser.generate_detection_results(patient_age=45, results=None)

    # Save and display the no-devices case
    with tempfile.NamedTemporaryFile(
        delete=False, suffix=".html", mode="w", encoding="utf-8"
    ) as temp_file:
        temp_file.write(html_content_no_devices)
        temp_file_path = temp_file.name
    webbrowser.open("file://" + os.path.realpath(temp_file_path))
