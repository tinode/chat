# Convert git tag like "v0.15.5-rc5-3-g2084bd63" to PEP 440 version like "0.15.5rc5.post3"

from subprocess import check_output

command = 'git describe --tags'

def git_version():
    line = check_output(command.split()).decode('utf-8').strip()
    if line.startswith("v"):
        line = line[1:]
    if '-rc' in line:
        line = line.replace('-rc', 'rc')
    if '-beta' in line:
        line = line.replace('-beta', 'b')
    if '-alpha' in line:
        line = line.replace('-alpha', 'a')
    if '-' in line:
        parts = line.split('-')
        line = parts[0] + '.post' + parts[1]
    return line

if __name__ == '__main__':
    with open('tinode_grpc/GIT_VERSION','w+') as fh:
        fh.write(git_version())
