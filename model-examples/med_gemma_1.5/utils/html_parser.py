from datetime import datetime


class HTMLParser:
    @staticmethod
    def generate_detection_results(results):
        """Generate clean HTML output for MedGemma medical image analysis.
        
        Args:
            results: Dictionary containing:
                    'diagnosis': Text analysis from MedGemma
                    'recommendations': dict with 'en' and 'fr' (optional)
        
        Returns:
            str: HTML formatted string containing the analysis results
        """
        diagnosis = results.get('diagnosis', '')
        
        # Convert newlines to paragraphs for better formatting
        paragraphs = [p.strip() for p in diagnosis.split('\n') if p.strip()]
        formatted_diagnosis = ''.join(f'<p>{p}</p>' for p in paragraphs)
        
        timestamp = datetime.now().strftime('%B %d, %Y at %H:%M')
        
        html_content = f"""<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Medical Image Analysis</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=Source+Serif+4:opsz,wght@8..60,400;8..60,600&family=Inter:wght@400;500;600&display=swap');
        
        * {{
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }}
        
        body {{
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            padding: 40px 20px;
            color: #1a1a2e;
        }}
        
        .container {{
            max-width: 800px;
            margin: 0 auto;
        }}
        
        .report {{
            background: #ffffff;
            border-radius: 16px;
            box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.25);
            overflow: hidden;
        }}
        
        .header {{
            background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%);
            padding: 32px 40px;
            color: #ffffff;
        }}
        
        .header-icon {{
            width: 48px;
            height: 48px;
            background: rgba(255, 255, 255, 0.1);
            border-radius: 12px;
            display: flex;
            align-items: center;
            justify-content: center;
            margin-bottom: 16px;
            font-size: 24px;
        }}
        
        .header h1 {{
            font-family: 'Source Serif 4', Georgia, serif;
            font-size: 28px;
            font-weight: 600;
            margin-bottom: 8px;
            letter-spacing: -0.5px;
        }}
        
        .header .subtitle {{
            color: rgba(255, 255, 255, 0.7);
            font-size: 14px;
            font-weight: 400;
        }}
        
        .content {{
            padding: 40px;
        }}
        
        .analysis-section {{
            margin-bottom: 32px;
        }}
        
        .section-label {{
            font-size: 11px;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 1.5px;
            color: #667eea;
            margin-bottom: 16px;
        }}
        
        .analysis-text {{
            font-family: 'Source Serif 4', Georgia, serif;
            font-size: 17px;
            line-height: 1.8;
            color: #2d3748;
        }}
        
        .analysis-text p {{
            margin-bottom: 16px;
        }}
        
        .analysis-text p:last-child {{
            margin-bottom: 0;
        }}
        
        .divider {{
            height: 1px;
            background: linear-gradient(90deg, transparent, #e2e8f0, transparent);
            margin: 32px 0;
        }}
        
        .disclaimer {{
            background: #f7fafc;
            border-radius: 12px;
            padding: 20px 24px;
            border-left: 4px solid #667eea;
        }}
        
        .disclaimer-title {{
            font-size: 13px;
            font-weight: 600;
            color: #4a5568;
            margin-bottom: 8px;
            display: flex;
            align-items: center;
            gap: 8px;
        }}
        
        .disclaimer-text {{
            font-size: 13px;
            color: #718096;
            line-height: 1.6;
        }}
        
        .footer {{
            padding: 24px 40px;
            background: #f7fafc;
            border-top: 1px solid #e2e8f0;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }}
        
        .timestamp {{
            font-size: 12px;
            color: #a0aec0;
        }}
        
        .badge {{
            display: inline-flex;
            align-items: center;
            gap: 6px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            font-size: 11px;
            font-weight: 600;
            padding: 6px 12px;
            border-radius: 20px;
            letter-spacing: 0.5px;
        }}
        
        @media (max-width: 600px) {{
            body {{
                padding: 20px 12px;
            }}
            
            .header, .content, .footer {{
                padding-left: 24px;
                padding-right: 24px;
            }}
            
            .header h1 {{
                font-size: 22px;
            }}
            
            .analysis-text {{
                font-size: 15px;
            }}
        }}
    </style>
</head>
<body>
    <div class="container">
        <div class="report">
            <div class="header">
                <div class="header-icon">🔬</div>
                <h1>Medical Image Analysis</h1>
                <p class="subtitle">AI-Powered Diagnostic Assessment</p>
            </div>
            
            <div class="content">
                <div class="analysis-section">
                    <div class="section-label">Analysis Results</div>
                    <div class="analysis-text">
                        {formatted_diagnosis if formatted_diagnosis else '<p>No analysis available.</p>'}
                    </div>
                </div>
                
                <div class="divider"></div>
                
                <div class="disclaimer">
                    <div class="disclaimer-title">
                        <span>⚠️</span> Important Notice
                    </div>
                    <p class="disclaimer-text">
                        This AI-generated analysis is intended for informational purposes only and should not replace professional medical advice. 
                        Please consult with a qualified healthcare provider for diagnosis and treatment decisions.
                    </p>
                </div>
            </div>
            
            <div class="footer">
                <span class="timestamp">Generated on {timestamp}</span>
                <span class="badge">
                    <span>✦</span> MedGemma
                </span>
            </div>
        </div>
    </div>
</body>
</html>"""
        
        return html_content
