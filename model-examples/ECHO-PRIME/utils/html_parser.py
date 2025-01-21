import base64
import tempfile
from typing import Dict, List
import webbrowser
import os
import pandas as pd

def generate_html_report(report_text: str, metrics: Dict[str, float], roc_thresholds_path: str, display: bool = False) -> str:
    """Generate an HTML report from the EchoPrime outputs.
    
    Args:
        report_text: The generated report text
        metrics: Dictionary of predicted metrics
        roc_thresholds_path: Path to the ROC thresholds CSV file
        display (bool): Whether to display the report in browser
        
    Returns:
        Path to the generated HTML file
    """
    # Split report into sections
    sections = report_text.split("[SEP]")
    sections = [s.strip() for s in sections if s.strip()]
    
    # Create HTML content
    html_content = """
    <!DOCTYPE html>
    <html>
    <head>
        <title>EchoPrime Report</title>
        <style>
            :root {
                --primary-color: rgb(0, 255, 0);
                --secondary-color: rgb(0, 200, 0);
                --background-color: #000000;
                --text-color: #ffffff;
                --border-color: #333333;
            }
            body { 
                font-family: Arial, sans-serif; 
                max-width: 1200px; 
                margin: 0 auto; 
                padding: 20px; 
                background-color: var(--background-color);
                color: var(--text-color);
            }
            h1 { 
                color: var(--primary-color); 
                text-align: center;
                font-family: 'Arial Black', Arial, sans-serif;
            }
            h2 {
                color: var(--primary-color);
                font-family: 'Arial Black', Arial, sans-serif;
                font-size: 20px;
                margin: 10px 0;
            }
            .report-section { 
                margin-bottom: 20px;
                background-color: #111111;
                padding: 15px;
                border-radius: 8px;
                border: 1px solid var(--border-color);
            }
            .section-title { 
                color: var(--primary-color); 
                font-weight: bold;
                font-family: 'Arial Black', Arial, sans-serif;
            }
            .metrics-grid { 
                display: grid; 
                grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); 
                gap: 10px; 
            }
            .metric-item { 
                background: #1a1a1a; 
                padding: 10px; 
                border-radius: 4px;
                display: flex;
                justify-content: space-between;
                border: 1px solid var(--border-color);
            }
            .metric-name { 
                color: var(--text-color); 
            }
            .metric-name.small-text {
                font-size: 14px;
            }
            .metric-value { 
                font-weight: bold; 
                color: var(--primary-color); 
            }
            .report-container, .metrics-container {
                background: #111111;
                padding: 20px;
                border-radius: 8px;
                box-shadow: 0 2px 4px rgba(0,255,0,0.1);
                margin-bottom: 20px;
                border: 1px solid var(--border-color);
            }
        </style>
    </head>
    <body>
        <h1>EchoPrime Report</h1>
        
        <div class="report-container">
            <h2>Generated Report</h2>
    """
    
    # Add report sections
    for section in sections:
        if ":" in section:
            title, content = section.split(":", 1)
            html_content += f"""
            <div class="report-section">
                <div class="section-title">{title}:</div>
                <div>{content.strip()}</div>
            </div>
            """
        else:
            html_content += f"""
            <div class="report-section">
                <div>{section.strip()}</div>
            </div>
            """
    
    # Add metrics sections
    html_content += """
        </div>
        
        <div class="metrics-container">
            <h2>Continuous Measurements</h2>
            <div class="metrics-grid">
    """
    
    # Read thresholds from CSV file
    thresholds_df = pd.read_csv(roc_thresholds_path, index_col=0)
    THRESHOLDS = thresholds_df.set_index('feature')['threshold'].to_dict()

    # First section: Continuous measurements
    # Handle PAP first
    for metric, value in metrics.items():
        if metric == "pulmonary_artery_pressure_continuous":
            formatted_name = metric.replace("_", " ").title()
            formatted_value = f"{value:.1f} mmHg"
            name_class = 'metric-name small-text'
            html_content += f"""
                <div class="metric-item">
                    <span class="{name_class}">{formatted_name}</span>
                    <span class="metric-value">{formatted_value}</span>
                </div>
            """
            break

    # Handle other continuous measurements, sorted by value
    continuous_metrics = [(m, v) for m, v in metrics.items() 
                         if m not in THRESHOLDS and m != "pulmonary_artery_pressure_continuous"]
    
    # Sort by value (converting to percentage if needed)
    continuous_metrics.sort(key=lambda x: x[1] * 100 if x[1] <= 1 else x[1], reverse=True)
    
    for metric, value in continuous_metrics:
        formatted_name = metric.replace("_", " ").title()
        percentage = value * 100 if value <= 1 else value
        formatted_value = f"{percentage:.1f}%"
        name_class = 'metric-name'
        html_content += f"""
            <div class="metric-item">
                <span class="{name_class}">{formatted_name}</span>
                <span class="metric-value">{formatted_value}</span>
            </div>
        """

    # Second section: Binary predictions
    html_content += """
            </div>
        </div>
        
        <div class="metrics-container">
            <h2>Binary Findings</h2>
            <div class="metrics-grid">
    """

    # Get all binary predictions and their status
    binary_predictions = []
    for metric, value in metrics.items():
        if metric in THRESHOLDS:
            formatted_name = metric.replace("_", " ").title()
            is_positive = value >= THRESHOLDS[metric]
            binary_predictions.append((metric, formatted_name, is_positive))
    
    # Sort binary predictions: True first, then alphabetically within each group
    binary_predictions.sort(key=lambda x: (-int(x[2]), x[1]))
    
    for _, formatted_name, is_positive in binary_predictions:
        binary_result = "Yes" if is_positive else "No"
        name_class = 'metric-name'
        html_content += f"""
            <div class="metric-item">
                <span class="{name_class}">{formatted_name}</span>
                <span class="metric-value">{binary_result}</span>
            </div>
        """

    html_content += """
            </div>
        </div>
    </body>
    </html>
    """
    
    # Convert to base64
    html_bytes = html_content.encode('utf-8')
    base64_html = base64.b64encode(html_bytes).decode('utf-8')

        # Display in browser if requested
    if display:
        temp_file = tempfile.NamedTemporaryFile(delete=False, suffix='.html')
        try:
            with open(temp_file.name, 'w', encoding='utf-8') as f:
                f.write(html_content)
            webbrowser.open('file://' + os.path.realpath(temp_file.name))
        finally:
            temp_file.close()

    return base64_html

def get_dummy_data():
    """Generate dummy data for testing the HTML report generation."""
    dummy_report = """Left Ventricle: Normal left ventricular size by linear cavity dimension. Normal left ventricular size by volume Normal left ventricular wall thickness. Normal left ventricular systolic function. LV Ejection Fraction is 59 %. [SEP]
    Right Ventricle: Normal right ventricular size. Normal right ventricular systolic function. [SEP]
    Left Atrium: The left atrium is normal in size. [SEP]
    Right Atrium: The right atrium is normal in size. [SEP]
    Mitral Valve: Mitral valve leaflets appear mildly thickened. There is trivial mitral regurgitation. [SEP]
    Aortic Valve: Trileaflet aortic valve. No aortic regurgitation seen. [SEP]"""

    dummy_metrics = {
        "ejection_fraction": 59.0,
        "wall_motion_hypokinesis": 0.0,
        "left_atrium_dilation": 0.0,
        "right_atrium_dilation": 0.0,
        "mitral_regurgitation": 0.1,
        "aortic_regurgitation": 0.0,
        "pulmonary_artery_pressure_continuous": 22.5
    }
    
    return dummy_report, dummy_metrics

def main():
    """Generate a sample HTML report using dummy data."""
    # Get dummy data
    report, metrics = get_dummy_data()
    roc_thresholds_path = 'models/auxiliary_files/roc_thresholds.csv'
    
    # Generate report
    generate_html_report(report, metrics, roc_thresholds_path, display=True)    

if __name__ == "__main__":
    main() 