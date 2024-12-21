import os
import sys
from pathlib import Path

def create_markdown_from_go_files(folder_path):
    # Check if folder exists
    if not os.path.isdir(folder_path):
        print(f"Error: '{folder_path}' is not a valid directory")
        sys.exit(1)

    # Create output markdown content
    markdown_content = "# Go Source Files\n\n"
    
    # Walk through the directory recursively
    for root, _, files in os.walk(folder_path):
        for file in files:
            if file.endswith('.go'):
                file_path = os.path.join(root, file)
                relative_path = os.path.relpath(file_path, folder_path)
                
                try:
                    with open(file_path, 'r', encoding='utf-8') as f:
                        content = f.read()
                        
                    # Add file name and content to markdown
                    markdown_content += f"## {relative_path}\n\n"
                    markdown_content += "```go\n"
                    markdown_content += content
                    markdown_content += "\n```\n\n"
                except Exception as e:
                    print(f"Error reading file {file_path}: {e}")

    # Write output markdown file
    output_file = "go_sources.md"
    try:
        with open(output_file, 'w', encoding='utf-8') as f:
            f.write(markdown_content)
        print(f"Successfully created {output_file}")
    except Exception as e:
        print(f"Error writing output file: {e}")

def main():
    if len(sys.argv) != 2:
        print("Usage: python script.py <folder_path>")
        sys.exit(1)
    
    folder_path = sys.argv[1]
    create_markdown_from_go_files(folder_path)

if __name__ == "__main__":
    main()