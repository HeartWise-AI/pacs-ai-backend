from time import sleep

from totalsegmentator.python_api import download_pretrained_weights

if __name__ == "__main__":
    """
    Download all pretrained weights (without commercial models)
    """
    # Only download the weights that are used in the "total" task to reduce the size of the Docker image
    for task_id in [ 
                    # 298,                      # Fastest
                    297,                        # Fast
                    # 291, 292, 293, 294, 295,  # Normal
                    # 258, 150, 260,
                    # 315, 299, 300, 850, 851, 852, 853, 775, 776, 777, 778,
                    # 779, 351, 913, 789, 527
                    ]:
        download_pretrained_weights(task_id)
        sleep(1)