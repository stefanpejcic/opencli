#!/usr/bin/python3

import importlib
import sys

def run_command(module_name, *args):
    module_name = module_name.replace('-', '.')  # Replace "/" with "." in module_name
    try:
        module = importlib.import_module(module_name)
        module.main(*args)
    except ImportError:
        print(f"Error: Module '{module_name}' not found.")
    except AttributeError:
        print(f"Error: Module '{module_name}' does not have a 'main' function.")

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: opencli <script> <arg1> [arg2] [arg3] ...")
    else:
        command = sys.argv[1]
        arguments = sys.argv[2:]
        run_command(command, *arguments)
