from flask import Flask, request, jsonify
import numpy as np

app = Flask(__name__)

@app.route('/process', methods=['POST'])
def process_pixels():
    pixel_data = request.json['pixels']
    
    # Convert to numpy array
    pixel_array = np.array(pixel_data)
    
    # Create a binary array where 0 is black (0-127) and 1 is white (128-255)
    binary_array = (pixel_array > 127).astype(int)
    
    return jsonify({"result": binary_array.tolist()})

if __name__ == "__main__":
    app.run(host='0.0.0.0', port=5000)