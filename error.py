import subprocess
import argparse

'''
Display logs for error code
'''

def extract_error_log_from_docker(error_code):
    container_name = 'openpanel'

    # Use subprocess to get the container logs
    try:
        result = subprocess.run(
            ['docker', 'logs', '--since=10m', container_name],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=True
        )
    except subprocess.CalledProcessError as e:
        return f"Error while fetching logs: {e.stderr}"

    logs = result.stdout.splitlines()

    result_log = []
    found_error_code = False

    for line in reversed(logs):
        if found_error_code:
            result_log.append(line.strip())
            if 'ERROR' in line:
                break
        elif error_code in line:
            found_error_code = True
            result_log.append(line.strip())

    result_log.reverse()

    if not found_error_code:
        return f"Error Code '{error_code}' not found in the {container_name} logs."

    return result_log

def main():
    parser = argparse.ArgumentParser(description="Extract error logs from the OpenPanel container by error code.")
    parser.add_argument("error_code", help="The error code to search for in the logs")

    args = parser.parse_args()
    error_log = extract_error_log_from_docker(args.error_code)

    # Print the result
    if isinstance(error_log, str):
        print(error_log)
    else:
        for line in error_log:
            print(line)

if __name__ == "__main__":
    main()
