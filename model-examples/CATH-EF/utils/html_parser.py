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


def create_age_distribution_plot(patient_age: int) -> str:
    """Create an enhanced age distribution plot with professional green theme"""
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
        line=dict(color='rgba(0, 128, 0, 0.8)', width=2),  # Dark green
        fillcolor='rgba(0, 128, 0, 0.1)'  # Light green fill
    ))

    # Add patient age point
    fig.add_trace(go.Scatter(
        x=[patient_age],
        y=[norm.pdf(patient_age, mean, std)],
        mode='markers',
        name='Patient Age',
        marker=dict(
            color='white',
            size=12,
            symbol='circle',
            line=dict(color='black', width=2)
        )
    ))

    # Update layout with professional styling
    fig.update_layout(
        title=dict(
            text='Age Distribution',
            font=dict(size=24, color='#333', family='Arial Black'),
            x=0.5,
            y=0.95
        ),
        xaxis=dict(
            title='Age (years)',
            gridcolor='rgba(0,0,0,0.1)',
            zerolinecolor='rgba(0,0,0,0.2)',
            tickfont=dict(family='Arial')
        ),
        yaxis=dict(
            title='Probability Density',
            gridcolor='rgba(0,0,0,0.1)',
            zerolinecolor='rgba(0,0,0,0.2)',
            tickfont=dict(family='Arial')
        ),
        paper_bgcolor='white',
        plot_bgcolor='white',
        showlegend=True,
        width=800,
        height=400,
        margin=dict(l=50, r=50, t=80, b=50),
        legend=dict(
            yanchor="top",
            y=0.99,
            xanchor="left",
            x=0.01,
            bgcolor='rgba(255,255,255,0.9)',
            bordercolor='black',
            borderwidth=1
        )
    )

    return pio.to_html(fig, full_html=False)


def create_series_summary_chart(vessels_data: List[Dict]) -> str:
    """Create a compact summary chart with professional green theme"""
    df = pd.DataFrame(vessels_data)

    fig = go.Figure()

    fig.add_trace(
        go.Table(
            header=dict(
                values=['Vessel Type', 'Series UID', 'LVEF'],
                fill_color='rgb(0, 70, 0)',  # Dark green header
                align='left',
                font=dict(color='white', size=12, family='Arial Black'),
                line_color='white',  # White lines between cells
                height=35
            ),
            cells=dict(
                values=[
                    df['vessel_type'],
                    df['series_uid'],
                    df.get('lvef', '').apply(lambda x: f"{x}%" if pd.notnull(x) else 'N/A')
                ],
                align='left',
                fill_color=[['white', '#f8f8f8'] * len(df)],  # Alternating white and light gray
                font=dict(color='black', size=11, family='Arial'),
                line_color='#e0e0e0',  # Light gray lines between cells
                height=30
            )
        )
    )

    fig.update_layout(
        title=dict(
            text='Series Summary',
            font=dict(size=20, color='black', family='Arial Black'),
            x=0.5
        ),
        width=800,
        margin=dict(l=20, r=20, t=40, b=20),
        paper_bgcolor='white'
    )

    return pio.to_html(fig, full_html=False)


def generate_vessel_report(vessels: list[tuple[str, str, Optional[float]]], 
                         patient_age: int, 
                         display: bool = True) -> str:
    """
    Generate a vessel report based on multiple vessels.
    
    Args:
        vessels (list[tuple]): List of tuples containing (series_number, vessel_type, lvef)
                             where lvef is optional and only used for left coronary vessels
        patient_age (int): Patient's age
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
                --primary-color: rgb(0, 70, 0);
                --secondary-color: rgb(0, 128, 0);
                --background-color: #ffffff;
                --text-color: #333333;
                --border-color: #e0e0e0;
            }}

            body {{
                font-family: Arial, sans-serif;
                margin: 0;
                padding: 20px;
                background-color: var(--background-color);
                color: var(--text-color);
            }}

            .report-container {{
                max-width: 1000px;
                margin: 0 auto;
                background-color: white;
                border-radius: 8px;
                box-shadow: 0 2px 4px rgba(0,0,0,0.1);
                padding: 30px;
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
                margin: 0;
                font-size: 32px;
                font-family: 'Arial Black', Arial, sans-serif;
            }}

            .summary-box {{
                background-color: #f8f8f8;
                border-radius: 8px;
                padding: 25px;
                margin: 20px 0;
                border: 1px solid var(--border-color);
            }}

            .lvef-average {{
                font-size: 24px;
                color: var(--primary-color);
                text-align: center;
                margin: 20px 0;
                font-weight: bold;
            }}

            .plot-container {{
                margin: 30px 0;
                padding: 20px;
                background-color: white;
                border-radius: 8px;
                border: 1px solid var(--border-color);
            }}

            h2 {{
                color: var(--primary-color);
                font-family: 'Arial Black', Arial, sans-serif;
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
                    <span class="value-label">Patient Age:</span> {patient_age} years | 
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