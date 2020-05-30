# Script for packaging generated model_pb2*.py into MidnightChat_grpc module.
import setuptools
from pkg_resources import resource_string

with open('README.md', 'r') as readme_file:
    long_description = readme_file.read()

with open("MidnightChat_grpc/GIT_VERSION", "r") as version_file:
    git_version = version_file.read().strip()
# git_version = resource_string(__name__, 'MidnightChat_grpc/GIT_VERSION').decode('ascii')

setuptools.setup(
    name="MidnightChat_grpc",
    version=git_version,
    author="MidnightChat Authors",
    author_email="info@MidnightChat.co",
    description="MidnightChat gRPC bindings.",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/MidnightChat/chat",
    packages=setuptools.find_packages(),
    install_requires=['protobuf>=3.6.1', 'grpcio>=1.19.0'],
    license="Apache 2.0",
    keywords="chat messaging messenger im MidnightChat",
    package_data={
        "": ["GIT_VERSION"],
    },
    classifiers=[
        "Programming Language :: Python :: 2",
        "Programming Language :: Python :: 2.7",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.5",
        "Programming Language :: Python :: 3.6",
        "License :: OSI Approved :: Apache Software License",
        "Operating System :: OS Independent",
        "Topic :: Communications :: Chat",
        "Intended Audience :: Developers",
    ],
)
