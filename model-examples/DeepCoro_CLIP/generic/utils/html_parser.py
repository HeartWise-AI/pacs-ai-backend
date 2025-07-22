import logging

from io import StringIO
from typing import Dict
from datetime import datetime


class HTMLParser:
    # Constants
    ARTERY_SYSTEMS = {
        'Right Coronary Artery (RCA) System': {
            'Proximal RCA': 'Proximal RCA',
            'Mid RCA': 'Mid RCA',
            'Distal RCA': 'Distal RCA',
            'Posterior Descending Artery': 'Posterior Descending Artery',
            'Posterolateral Branch': 'Posterolateral Branch'
        },
        'Left Coronary Artery (LCA) System': {
            'Left Main Branch': 'Left Main Branch',
            'Proximal LAD': 'Proximal LAD',
            'Mid LAD': 'Mid LAD',
            'Distal LAD': 'Distal LAD',
            'D1 Branch': 'D1 Branch',
            'D2 Branch': 'D2 Branch',
            'Proximal LCX': 'Proximal LCX',
            'Distal LCX': 'Distal LCX',
            'Mid LCX': 'Mid LCX',
            'OM1 (Obtuse Marginal 1)': 'OM1 (Obtuse Marginal 1)',
            'OM2 (Obtuse Marginal 2)': 'OM2 (Obtuse Marginal 2)',
        },
        'Other': {
            'Branch Vessel': 'Branch Vessel',
            'LVp': 'LVp'
        }
    }
    
    DIAGNOSIS_TYPES = ['stenosis', 'cto', 'calcif', 'thrombus']
    DIAGNOSIS_VALUES = {
        'stenosis': 'blocked',
        'cto': 'cto', 
        'calcif': 'calcified',
        'thrombus': 'thrombus'
    }
    
    # System colors for detailed analysis
    SYSTEM_COLORS = {
        'rca': '#e67e22',  # Orange
        'lca': '#3498db',  # Blue
        'other': '#9b59b6'  # Purple
    }

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
    def _validate_input(results: Dict) -> bool:
        """Validate input data structure."""
        if not isinstance(results, dict):
            logging.error("Input results must be a dictionary")
            return False
        
        required_keys = ['probability']
        for key in required_keys:
            if key not in results:
                logging.error(f"Missing required key: {key}")
                return False
        
        if not isinstance(results['probability'], dict):
            logging.error("Probability data must be a dictionary")
            return False
        
        return True

    @staticmethod
    def _classify_arteries(results: Dict) -> Dict[str, Dict]:
        """Classify arteries by system (RCA, LCA, Other)."""
        try:
            classified = {
                'rca': {},
                'lca': {},
                'other': {}
            }
            
            for artery_name, data in results['probability'].items():
                if artery_name in HTMLParser.ARTERY_SYSTEMS['Right Coronary Artery (RCA) System']:
                    classified['rca'][artery_name] = data
                elif artery_name in HTMLParser.ARTERY_SYSTEMS['Left Coronary Artery (LCA) System']:
                    classified['lca'][artery_name] = data
                elif artery_name in HTMLParser.ARTERY_SYSTEMS['Other']:
                    classified['other'][artery_name] = data
                else:
                    logging.warning(f"Unknown artery: {artery_name}")
            
            logging.info(f"Classified arteries - RCA: {len(classified['rca'])}, LCA: {len(classified['lca'])}, Other: {len(classified['other'])}")
            return classified
            
        except Exception as e:
            logging.error(f"Error classifying arteries: {e}")
            return {'rca': {}, 'lca': {}, 'other': {}}

    @staticmethod
    def _count_diagnoses(arteries: Dict) -> Dict[str, int]:
        """Count diagnoses for a group of arteries."""
        try:
            counts = {diagnosis_type: 0 for diagnosis_type in HTMLParser.DIAGNOSIS_TYPES}
            
            for artery_name, artery_data in arteries.items():
                for diagnosis_type in HTMLParser.DIAGNOSIS_TYPES:
                    diagnosis_key = f'diagnosis_{diagnosis_type}'
                    if (diagnosis_key in artery_data and 
                        artery_data[diagnosis_key] == HTMLParser.DIAGNOSIS_VALUES[diagnosis_type]):
                        counts[diagnosis_type] += 1
            
            return counts
            
        except Exception as e:
            logging.error(f"Error counting diagnoses: {e}")
            return {diagnosis_type: 0 for diagnosis_type in HTMLParser.DIAGNOSIS_TYPES}

    @staticmethod
    def _calculate_total_counts(classified_arteries: Dict[str, Dict]) -> Dict[str, int]:
        """Calculate total counts across all artery systems."""
        try:
            all_counts = {}
            for system_name, system_arteries in classified_arteries.items():
                system_counts = HTMLParser._count_diagnoses(system_arteries)
                for diagnosis_type, count in system_counts.items():
                    all_counts[diagnosis_type] = all_counts.get(diagnosis_type, 0) + count
            
            logging.info(f"Total counts: {all_counts}")
            return all_counts
            
        except Exception as e:
            logging.error(f"Error calculating total counts: {e}")
            return {diagnosis_type: 0 for diagnosis_type in HTMLParser.DIAGNOSIS_TYPES}

    @staticmethod
    def _determine_colors(total_counts: Dict[str, int]) -> Dict[str, str]:
        """Determine card colors based on total counts."""
        try:
            colors = {}
            for diagnosis_type in HTMLParser.DIAGNOSIS_TYPES:
                colors[diagnosis_type] = "red" if total_counts.get(diagnosis_type, 0) > 1 else "green"
            return colors
            
        except Exception as e:
            logging.error(f"Error determining colors: {e}")
            return {diagnosis_type: "green" for diagnosis_type in HTMLParser.DIAGNOSIS_TYPES}

    @staticmethod
    def _generate_status_card(diagnosis_type: str, count: int, total: int, color: str) -> str:
        """Generate HTML for a single status card."""
        try:
            title = diagnosis_type.title()
            return f"""
                    <div class="status-card {diagnosis_type} {color}">
                        <div class="card-content">
                            <h3 class="card-title">{title}</h3>
                            <div class="card-number">{count} / {total}</div>
                            <div class="card-label">vessels affected</div>
                        </div>
                    </div>"""
        except Exception as e:
            logging.error(f"Error generating status card: {e}")
            return ""

    @staticmethod
    def _generate_system_section(system_name: str, arteries: Dict, counts: Dict[str, int], colors: Dict[str, str]) -> str:
        """Generate HTML section for a coronary artery system."""
        cards_html = "".join(
            HTMLParser._generate_status_card(diagnosis_type, counts.get(diagnosis_type, 0), len(arteries), colors.get(diagnosis_type, "green"))
            for diagnosis_type in HTMLParser.DIAGNOSIS_TYPES
        )
        
        return f"""
                <div style="text-align: center; margin: 30px 0;">
                    <h1 style="color: #2c3e50; font-size: 28px; font-weight: 700;">{system_name}</h1>
                </div>

                <div class="status-cards">
                    {cards_html}
                </div>"""

    @staticmethod
    def _get_css_styles() -> str:
        """Return the CSS styles for the HTML report."""
        return HTMLParser.CSS_STYLES

    @staticmethod
    def _generate_recommendations_section(recommendations: Dict[str, str]) -> str:
        """Generate HTML for the clinical recommendations section."""
        recommendations_en = recommendations.get("en", "")
        recommendations_fr = recommendations.get("fr", "")
        
        en_section = f'<div style="margin: 15px 0;"><span style="font-weight: bold; color: #3498db; text-transform: uppercase; font-size: 13px; margin-bottom: 8px; display: block;">English</span><div style="font-size: 16px; line-height: 1.6;">{recommendations_en}</div></div>' if recommendations_en else ''
        fr_section = f'<div style="margin: 15px 0;"><span style="font-weight: bold; color: #3498db; text-transform: uppercase; font-size: 13px; margin-bottom: 8px; display: block;">Français</span><div style="font-size: 16px; line-height: 1.6;">{recommendations_fr}</div></div>' if recommendations_fr else ''
        
        return f"""
                <!-- Clinical Recommendations Section -->
                <div style="margin: 40px 0; padding: 25px; background: linear-gradient(135deg, #3498db15, #3498db05); border-left: 4px solid #3498db; border-radius: 8px;">
                    <h2 style="color: #3498db; margin: 0 0 20px 0; font-size: 24px; font-weight: 600;">Clinical Recommendations</h2>            
                    {en_section}
                    {fr_section}
                </div>"""

    @staticmethod
    def _generate_detailed_diagnosis_section(diagnosis_data: Dict) -> str:
        """Generate HTML for detailed diagnosis analysis section."""
        if not diagnosis_data:
            return ""
        
        # Classify arteries by system
        classified_arteries = HTMLParser._classify_arteries({"probability": diagnosis_data})
        
        # Generate sections for each artery system
        sections = []
        
        # RCA Section
        rca_arteries = classified_arteries.get('rca', {})
        if rca_arteries:
            rca_section = HTMLParser._generate_system_detailed_section("Right Coronary Artery", rca_arteries, HTMLParser.SYSTEM_COLORS['rca'])
            sections.append(rca_section)
        
        # LCA Section
        lca_arteries = classified_arteries.get('lca', {})
        if lca_arteries:
            lca_section = HTMLParser._generate_system_detailed_section("Left Coronary Artery", lca_arteries, HTMLParser.SYSTEM_COLORS['lca'])
            sections.append(lca_section)
        
        # Other Section
        other_arteries = classified_arteries.get('other', {})
        if other_arteries:
            other_section = HTMLParser._generate_system_detailed_section("Other Vessels", other_arteries, HTMLParser.SYSTEM_COLORS['other'])
            sections.append(other_section)
        
        return ''.join(sections)

    @staticmethod
    def _build_vessel_html(vessel_name: str, vessel_data: Dict, color: str) -> str:
        # Extract probabilities and convert to percentages
        stenosis_prob = vessel_data.get('stenosis_prob', 0) * 100
        calcif_prob = vessel_data.get('calcif_prob', 0) * 100
        cto_prob = vessel_data.get('cto_prob', 0) * 100
        thrombus_prob = vessel_data.get('thrombus_prob', 0) * 100
        
        # Get diagnoses
        stenosis_diagnosis = vessel_data.get('diagnosis_stenosis', 'normal')
        calcif_diagnosis = vessel_data.get('diagnosis_calcif', 'normal')
        cto_diagnosis = vessel_data.get('diagnosis_cto', 'normal')
        thrombus_diagnosis = vessel_data.get('diagnosis_thrombus', 'normal')
        
        # Determine status colors
        stenosis_color = "red" if stenosis_diagnosis == 'blocked' else "green"
        calcif_color = "red" if calcif_diagnosis == 'calcified' else "green"
        cto_color = "red" if cto_diagnosis == 'cto' else "green"
        thrombus_color = "red" if thrombus_diagnosis == 'thrombus' else "green"
        
        return f"""
                <div style="background: #ffffff; border-radius: 8px; padding: 20px; margin: 15px 0; box-shadow: 0 2px 4px rgba(0,0,0,0.1); border-left: 4px solid {color};">
                    <h3 style="color: #2c3e50; margin: 0 0 15px 0; font-size: 20px; font-weight: 600;">{vessel_name}</h3>
                    
                    <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px;">
                        <div style="background: linear-gradient(135deg, #{'e74c3c' if stenosis_color == 'red' else '27ae60'}15, #{'e74c3c' if stenosis_color == 'red' else '27ae60'}05); padding: 15px; border-radius: 6px; border-left: 3px solid #{'#e74c3c' if stenosis_color == 'red' else '#27ae60'};">
                            <div style="font-weight: 600; color: #2c3e50; margin-bottom: 5px;">Stenosis</div>
                            <div style="font-size: 24px; font-weight: bold; color: #{'#e74c3c' if stenosis_color == 'red' else '#27ae60'};">{stenosis_prob:.1f}%</div>
                            <div style="font-size: 12px; color: #7f8c8d; text-transform: uppercase;">{stenosis_diagnosis}</div>
                        </div>
                        
                        <div style="background: linear-gradient(135deg, #{'e74c3c' if calcif_color == 'red' else '27ae60'}15, #{'e74c3c' if calcif_color == 'red' else '27ae60'}05); padding: 15px; border-radius: 6px; border-left: 3px solid #{'#e74c3c' if calcif_color == 'red' else '#27ae60'};">
                            <div style="font-weight: 600; color: #2c3e50; margin-bottom: 5px;">Calcification</div>
                            <div style="font-size: 24px; font-weight: bold; color: #{'#e74c3c' if calcif_color == 'red' else '#27ae60'};">{calcif_prob:.1f}%</div>
                            <div style="font-size: 12px; color: #7f8c8d; text-transform: uppercase;">{calcif_diagnosis}</div>
                        </div>
                        
                        <div style="background: linear-gradient(135deg, #{'e74c3c' if cto_color == 'red' else '27ae60'}15, #{'e74c3c' if cto_color == 'red' else '27ae60'}05); padding: 15px; border-radius: 6px; border-left: 3px solid #{'#e74c3c' if cto_color == 'red' else '#27ae60'};">
                            <div style="font-weight: 600; color: #2c3e50; margin-bottom: 5px;">CTO</div>
                            <div style="font-size: 24px; font-weight: bold; color: #{'#e74c3c' if cto_color == 'red' else '#27ae60'};">{cto_prob:.1f}%</div>
                            <div style="font-size: 12px; color: #7f8c8d; text-transform: uppercase;">{cto_diagnosis}</div>
                        </div>
                        
                        <div style="background: linear-gradient(135deg, #{'e74c3c' if thrombus_color == 'red' else '27ae60'}15, #{'e74c3c' if thrombus_color == 'red' else '27ae60'}05); padding: 15px; border-radius: 6px; border-left: 3px solid #{'#e74c3c' if thrombus_color == 'red' else '#27ae60'};">
                            <div style="font-weight: 600; color: #2c3e50; margin-bottom: 5px;">Thrombus</div>
                            <div style="font-size: 24px; font-weight: bold; color: #{'#e74c3c' if thrombus_color == 'red' else '#27ae60'};">{thrombus_prob:.1f}%</div>
                            <div style="font-size: 12px; color: #7f8c8d; text-transform: uppercase;">{thrombus_diagnosis}</div>
                        </div>
                    </div>
                </div>"""

    @staticmethod
    def _generate_system_detailed_section(system_name: str, arteries: Dict, color: str) -> str:
        """Generate detailed analysis section for a specific artery system."""
        try:
            vessel_details = ''.join(
                HTMLParser._build_vessel_html(
                    vessel_name, vessel_data, color
                ) for vessel_name, vessel_data in arteries.items()
            )
            
            return f"""
                    <!-- Detailed {system_name} Analysis Section -->
                    <div style="margin: 40px 0; padding: 25px; background: linear-gradient(135deg, {color}15, {color}05); border-left: 4px solid {color}; border-radius: 8px;">
                        <h2 style="color: {color}; margin: 0 0 20px 0; font-size: 24px; font-weight: 600;">Detailed {system_name} Analysis</h2>
                        <p style="color: #2c3e50; margin-bottom: 20px; font-size: 16px;">Individual vessel analysis with probability percentages and AI diagnoses for each {system_name} segment.</p>
                        
                        {vessel_details}
                    </div>"""
                    
        except Exception as e:
            logging.error(f"Error generating detailed section for {system_name}: {e}")
            return ""

    @staticmethod
    def generate_detection_results(results: Dict) -> str:
        """Generate HTML output with coronary stenosis detection results.

        Args:
            results: Dictionary containing:
                    'probability': dict with prediction values for each artery
                    'diagnosis': JSON string with diagnosis for each head
                    'recommendations': dict with 'en' and 'fr' recommendations

        Returns:
            str: HTML formatted string containing the classification result
        """
        # Validate input
        if not HTMLParser._validate_input(results):
            logging.error("Invalid input data for HTML generation.")
            return "Error: Invalid input data."

        # Use StringIO for the entire HTML generation
        buffer = StringIO()
        
        # Write  HTML header
        buffer.write("""<!DOCTYPE html>
        <html>
        <head>
            <title>Coronary Stenosis Detection Report</title>
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
                    <h1>Coronary Analysis Results Report</h1>
                    <p class="subtitle">AI-Powered Cardiac Analysis</p>
                </div>
                
                """)
        
        # Generate sections efficiently
        classified_arteries = HTMLParser._classify_arteries(results)
        system_counts = {system: HTMLParser._count_diagnoses(arteries) 
                        for system, arteries in classified_arteries.items()}
        total_counts = HTMLParser._calculate_total_counts(classified_arteries)
        colors = HTMLParser._determine_colors(total_counts)
        
        # Write sections
        system_names = {
            'rca': 'Right Coronary Artery (RCA) System',
            'lca': 'Left Coronary Artery (LCA) System', 
            'other': 'Other Vessels'
        }
        
        for system_key, system_name in system_names.items():
            if classified_arteries[system_key]:  # Only generate section if arteries exist
                section = HTMLParser._generate_system_section(
                    system_name, 
                    classified_arteries[system_key], 
                    system_counts[system_key], 
                    colors
                )
                buffer.write(section)
        
        # Write remaining sections
        buffer.write(HTMLParser._generate_recommendations_section(results.get("recommendations", {})))
        buffer.write(HTMLParser._generate_detailed_diagnosis_section(results.get("probability", {})))
        
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
