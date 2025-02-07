import string
import plotly.graph_objects as go
import numpy as np
from scipy.stats import norm
import plotly.io as pio
import webbrowser
import os
import tempfile
from typing import Optional
from enum import Enum
import pandas as pd
import base64
from typing import List, Dict, Optional, Tuple


def create_age_distribution_plot(patient_age: Optional[int]) -> str:
    """Create an enhanced age distribution plot with dark theme and green accents"""
    x = np.linspace(0, 100, 1000)
    mean = 50
    std = 15
    y = norm.pdf(x, mean, std)

    fig = go.Figure()

    # Add filled distribution curve
    fig.add_trace(go.Scatter(
        x=x,
        y=y,
        fill='tozeroy',
        name='Age Distribution',
        line=dict(color='rgba(0, 255, 0, 0.8)', width=2),  # Bright green
        fillcolor='rgba(0, 255, 0, 0.1)'  # Light green fill
    ))

    # Add patient age point only if age is provided
    if patient_age is not None:
        fig.add_trace(go.Scatter(
            x=[patient_age],
            y=[norm.pdf(patient_age, mean, std)],
            mode='markers',
            name='Patient Age',
            marker=dict(
                color='rgb(0, 255, 0)',
                size=12,
                symbol='circle',
                line=dict(color='white', width=2)
            )
        ))

    # Update layout with dark theme styling
    fig.update_layout(
        title=dict(
            text='Age Distribution',
            font=dict(size=24, color='#00ff00', family='Arial Black'),
            x=0.5,
            y=0.95
        ),
        xaxis=dict(
            title='Age (years)',
            gridcolor='rgba(255,255,255,0.1)',
            zerolinecolor='rgba(255,255,255,0.2)',
            tickfont=dict(family='Arial', color='white'),
            title_font=dict(color='white')
        ),
        yaxis=dict(
            title='Probability Density',
            gridcolor='rgba(255,255,255,0.1)',
            zerolinecolor='rgba(255,255,255,0.2)',
            tickfont=dict(family='Arial', color='white'),
            title_font=dict(color='white')
        ),
        paper_bgcolor='black',
        plot_bgcolor='black',
        showlegend=True,
        width=800,
        height=400,
        margin=dict(l=50, r=50, t=80, b=50),
        legend=dict(
            yanchor="top",
            y=0.99,
            xanchor="left",
            x=0.01,
            bgcolor='rgba(0,0,0,0.9)',
            bordercolor='#00ff00',
            borderwidth=1,
            font=dict(color='white')
        )
    )

    return pio.to_html(fig, full_html=False)


def create_series_summary_chart(vessels_data: List[Dict]) -> str:
    """Create a compact summary chart with dark theme and green accents"""
    df = pd.DataFrame(vessels_data)

    # Calculate dynamic height based on number of rows (30px per row plus header)
    dynamic_height = len(df) * 35 + 45

    fig = go.Figure()

    fig.add_trace(
        go.Table(
            header=dict(
                values=['Vessel Type', 'Series UID', 'LVEF'],
                fill_color='rgb(0, 200, 0)',  # Bright green header
                align='center',  # Center align header
                font=dict(color='black', size=12, family='Arial Black'),
                line_color='black',
                height=35
            ),
            cells=dict(
                values=[
                    df['vessel_type'],
                    df['series_uid'],
                    df.get('lvef', '').apply(lambda x: f"{x}%" if pd.notnull(x) else 'N/A')
                ],
                align='center',  # Center align cells
                fill_color=[['rgb(20,20,20)', 'rgb(30,30,30)'] * len(df)],  # Alternating dark grays
                font=dict(color='white', size=11, family='Arial'),
                line_color='#333333',
                height=30
            )
        )
    )

    fig.update_layout(
        title=dict(
            text='Series Summary',
            font=dict(size=20, color='#00ff00', family='Arial Black'),
            x=0.5
        ),
        width=800,
        height=dynamic_height + 40,  # Add some padding
        margin=dict(l=20, r=20, t=40, b=20),
        paper_bgcolor='black'
    )

    return pio.to_html(fig, full_html=False)


def generate_vessel_report(vessels: list[tuple[str, str, Optional[float]]], 
                         patient_age: Optional[int], 
                         display: bool = True) -> str:
    """
    Generate a vessel report based on multiple vessels.
    
    Args:
        vessels (list[tuple]): List of tuples containing (series_number, vessel_type, lvef)
                             where lvef is optional and only used for left coronary vessels
        patient_age (Optional[int]): Patient's age, can be None if not available
        display (bool): Whether to display the report in browser
    
    Returns:
        str: Base64 encoded HTML report
    """
    
    # Create vessel data
    vessel_data = []
    for series_number, vessel_type, lvef in vessels:
        # Only include LVEF if it's a left coronary vessel and LVEF is provided
        vessel_lvef = lvef if (lvef is not None and "Left Coronary" in vessel_type) else None
        
        vessel_data.append({
            'vessel_type': vessel_type,
            'series_uid': series_number,
            'lvef': vessel_lvef
        })

    # Generate plots
    age_plot_html = create_age_distribution_plot(patient_age)
    series_chart_html = create_series_summary_chart(vessel_data)

    # Calculate LVEF stats
    lvef_values = [v['lvef'] for v in vessel_data if v['lvef'] is not None]
    avg_lvef = round(sum(lvef_values) / len(lvef_values), 1) if lvef_values else 0.0
    lvef_count = len(lvef_values)

    # Create HTML content
    html_content = f"""
    <!DOCTYPE html>
    <html>
    <head>
        <title>Vessel Analysis Report</title>
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

            .report-container {{
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

            .summary-box {{
                background-color: #1a1a1a;
                border-radius: 8px;
                padding: 20px;
                margin: 15px 0;
                border: 1px solid var(--border-color);
            }}

            .lvef-average {{
                font-size: 22px;
                color: var(--primary-color);
                text-align: center;
                margin: 15px 0;
                font-weight: bold;
            }}

            .plot-container {{
                margin: 20px auto;  /* Changed from '20px 0' to center horizontally */
                padding: 15px;
                background-color: #111111;
                border-radius: 8px;
                border: 1px solid var(--border-color);
                display: flex;  /* Added for centering content */
                justify-content: center;  /* Center horizontally */
                align-items: center;  /* Center vertically */
            }}

            h2 {{
                color: var(--primary-color);
                font-family: 'Arial Black', Arial, sans-serif;
                font-size: 20px;
                margin: 10px 0;
            }}

            .value-label {{
                font-weight: bold;
                color: var(--primary-color);
            }}

            .highlight {{
                color: var(--secondary-color);
            }}
        </style>
    </head>
    <body>
        <div class="report-container">
            <div class="header">
                <h1>Vessel Analysis Report</h1>
                <p style="color: var(--text-color);">
                    <span class="value-label">Patient Age:</span> {f"{patient_age} years" if patient_age is not None else "N/A"} | 
                </p>
            </div>

            <div class="summary-box">
                <h2>Summary</h2>
                <p><span class="value-label">Number of vessels analyzed:</span> {len(vessels)}</p>
                <p><span class="value-label">Number of LVEF measurements:</span> {lvef_count}</p>
                {f'<div class="lvef-average"><span class="highlight">Average LVEF: {avg_lvef}%</span></div>' if lvef_count > 0 else ''}
            </div>

            <div class="plot-container">
                {series_chart_html}
            </div>

            <div class="plot-container">
                {age_plot_html}
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

# Example usage:
if __name__ == "__main__":
    # Example with multiple vessels
    vessels_data = [
        ("Series 1", 'Left Coronary', 55.5),
        ("Series 2", 'Left Coronary', 60.0),
        ("Series 3", 'Right Coronary', None),
        ("Series 4", 'Left Coronary', 52.3),
    ]
    
    report = generate_vessel_report(
        vessels=vessels_data,
        patient_age=65,
        display=True
    )