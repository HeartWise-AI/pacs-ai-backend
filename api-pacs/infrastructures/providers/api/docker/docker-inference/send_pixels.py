import requests
import time
import numpy as np

def generate_pixel_data(width, height):
    # Generate a simple pattern: top-left quarter is black, rest is white
    pixel_data = np.zeros((height, width), dtype=np.uint8)
    pixel_data[height//2:, width//2:] = 255
    return pixel_data.tolist()

def send_pixels():
    url = 'http://processor:5000/process'
    pixel_data = generate_pixel_data(10, 10)  # 10x10 pixel image
    
    # Wait for the processor service to be ready
    for _ in range(30):  # Try for 30 seconds
        try:
            response = requests.post(url, json={'pixels': pixel_data})
            if response.status_code == 200:
                print("Pixels processed successfully!")
                print("Input pixel data:")
                print(np.array(pixel_data))
                print("\nResult:")
                print(np.array(response.json()['result']))
                return
        except requests.exceptions.ConnectionError:
            time.sleep(1)
    
    print("Failed to connect to the processor service")

if __name__ == "__main__":
    send_pixels()