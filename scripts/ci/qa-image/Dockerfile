# QA bootstrap image: Ubuntu 26.04 (the same release as GitHub's ubuntu-latest runner) with the OCR
# and font tools the QA harness needs, installed once. QA runs use this image and download nothing
# from Ubuntu. Packages come through the mirror list beside this file, tried in order, so one
# unreachable Ubuntu server cannot stop the build. Changing either file builds a new image.
FROM ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c
COPY mirrors.txt /etc/apt/togavsu-mirrors.txt
RUN printf '%s\n' 'Types: deb' 'URIs: mirror+file:/etc/apt/togavsu-mirrors.txt' \
        'Suites: resolute resolute-updates resolute-backports resolute-security' \
        'Components: main universe restricted multiverse' \
        'Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg' > /etc/apt/sources.list.d/ubuntu.sources \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y \
        tesseract-ocr tesseract-ocr-eng fonts-dejavu-core python3 python3-venv git ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && tesseract --version
