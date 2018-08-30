# Script for packaging generated model_pb2*.py into tinode_grpc module.

import setuptools
from subprocess import Popen, PIPE

with open("README.md", "r") as fh:
    long_description = fh.read()

# Convert git tag like "v0.15.5-rc5-3-g2084bd63" to PEP 440 version like "0.15.5rc5.post3"
def git_version():
    p = Popen(['git', 'describe', '--tags'], stdout=PIPE, stderr=PIPE)
    p.stderr.close()
    line = p.stdout.readlines()[0].decode("ascii").strip()
    if line.startswith("v"):
        line = line[1:]
    if "-rc" in line:
        line = line.replace("-rc", "rc")
    if "-" in line:
        parts = line.split("-")
        line = parts[0] + ".post" + parts[1]
    return line

setuptools.setup(
    name="tinode_grpc",
    version=git_version(),
    author="Tinode Authors",
    author_email="gene@tinode.co",
    description="Tinode gRPC bindings.",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/tinode/chat",
    packages=setuptools.find_packages(),
    install_requires=['grpcio>=1.9.1'],
    license="Apache 2.0",
    keywords="chat messaging messenger im tinode",
    classifiers=(
        "Programming Language :: Python :: 2",
        "Programming Language :: Python :: 2.7",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.6",
        "License :: OSI Approved :: Apache Software License",
        "Operating System :: OS Independent",
        "Topic :: Communications :: Chat",
        "Intended Audience :: Developers",
    ),
)
