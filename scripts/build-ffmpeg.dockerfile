# Build static FFmpeg + FFprobe with all codecs and filters needed by VEO.
#
# Produces Linux binaries for cloud/CI deployment.
# For macOS native binaries, use build-ffmpeg-macos.sh instead.
#
# Single arch (matches your Docker host):
#   docker build -f scripts/build-ffmpeg.dockerfile -o ./bin/ffmpeg .
#
# Multi-arch (for cloud deployment):
#   docker buildx build -f scripts/build-ffmpeg.dockerfile \
#     --platform linux/amd64,linux/arm64 \
#     -o type=local,dest=./bin/ffmpeg .
#
# Included codecs/filters:
#   x264, x265, SVT-AV1, dav1d, libvpx/VP9, libvmaf, opus

FROM debian:bookworm-slim AS build

ARG DEBIAN_FRONTEND=noninteractive

ENV VVENC_TAG=v1.13.1-rc1
ENV XEVE_TAG=v0.5.1
ENV XEVD_TAG=v0.5.0

# Build tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    autoconf automake build-essential cmake git-core \
    libtool meson nasm ninja-build pkg-config wget yasm curl xxd\
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# ── x264 ──────────────────────────────────────────────────────────
RUN git clone --depth 1 https://github.com/mirror/x264.git && \
    cd x264 && \
    ./configure --prefix=/usr/local --enable-static --enable-pic --disable-cli && \
    make -j$(nproc) && make install

# ── x265 ──────────────────────────────────────────────────────────
RUN git clone --depth 1 -b 4.1 https://bitbucket.org/multicoreware/x265_git.git x265 && \
    cd x265/build/linux && \
    cmake -G "Unix Makefiles" \
        -DCMAKE_INSTALL_PREFIX=/usr/local \
        -DENABLE_SHARED=OFF \
        -DENABLE_CLI=OFF \
        -DSTATIC_LINK_CRT=ON \
        ../../source && \
    make -j$(nproc) && make install

# ── SVT-AV1 ──────────────────────────────────────────────────────
RUN git clone --depth 1 -b v4.0.0 https://gitlab.com/AOMediaCodec/SVT-AV1.git && \
    cd SVT-AV1/Build && \
    cmake -G "Unix Makefiles" \
        -DCMAKE_INSTALL_PREFIX=/usr/local \
        -DBUILD_SHARED_LIBS=OFF \
        -DBUILD_APPS=OFF \
        -DBUILD_DEC=ON \
        .. && \
    make -j$(nproc) && make install

# ── dav1d (AV1 decoder) ──────────────────────────────────────────
RUN git clone --depth 1 -b 1.5.0 https://github.com/videolan/dav1d.git && \
    cd dav1d && \
    meson setup build --prefix=/usr/local --default-library=static --buildtype=release && \
    ninja -C build && ninja -C build install

# ── libvpx (VP9) ─────────────────────────────────────────────────
RUN git clone --depth 1 -b v1.15.0 https://chromium.googlesource.com/webm/libvpx.git && \
    cd libvpx && \
    ./configure --prefix=/usr/local \
        --enable-static --disable-shared \
        --enable-vp9-highbitdepth \
        --disable-examples --disable-unit-tests --disable-docs && \
    make -j$(nproc) && make install

    # Build vvenc
RUN git clone https://github.com/fraunhoferhhi/vvenc.git vvenc && \
    cd vvenc && \
    git checkout $VVENC_TAG && \
    mkdir build && \
    cd build && \
    cmake .. -DCMAKE_INSTALL_PREFIX=/usr/local -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF && \
    make -j$(nproc) && \
    make install && \
    ldconfig

    # Build xeve
# RUN git clone https://github.com/mpeg5/xeve && \
#     cd xeve && \
#     git checkout tags/$XEVE_TAG && \
#     mkdir build && \
#     cd build && \
#     cmake .. -DCMAKE_INSTALL_PREFIX=/usr/local -DENABLE_SHARED=OFF&& \
#     make -j$(nproc) && \
#     make install && \
#     ldconfig && \
#     make package && \
#      ldconfig

# # Build xevd
# RUN git clone https://github.com/mpeg5/xevd.git && \
#     cd xevd && \
#     git checkout tags/$XEVD_TAG && \
#     mkdir build && \
#     cd build && \
#     cmake .. -DCMAKE_INSTALL_PREFIX=/usr/local -DENABLE_SHARED=OFF && \
#     make -j$(nproc) && \
#     make install && \
#     ldconfig && \
#     make package && \
#     ldconfig
# RUN echo "/usr/local/lib" > /etc/ld.so.conf.d/xevd.conf && ldconfig

RUN wget https://www.nasm.us/pub/nasm/releasebuilds/2.16.03/nasm-2.16.03.tar.bz2 && \
    tar xjvf nasm-2.16.03.tar.bz2 && \
    cd nasm-2.16.03 && \
    ./autogen.sh && \
    ./configure --prefix="/usr" && \
    make -j$(nproc) && \
    make install



# ── libvmaf ───────────────────────────────────────────────────────
# Built as static lib; FFmpeg links against it via --enable-libvmaf.
# libvmaf 3.0 embeds VMAF models in the binary — no external model files needed.
#7e16db0a2ccdd8547680b9ed0b3e52691e8ecee7

RUN git clone --depth 1 -b v3.0.0 https://github.com/Netflix/vmaf.git && \
    cd vmaf/libvmaf && \
    meson setup build \
        --prefix=/usr/local \
        --default-library=static \
        --buildtype=release \
        -Dbuilt_in_models=true && \
    ninja -C build && ninja -C build install && ldconfig

# RUN git clone https://github.com/Netflix/vmaf.git && \
#       cd vmaf/libvmaf &&  git checkout 7e16db0a2ccdd8547680b9ed0b3e52691e8ecee7 \
#      meson setup build --prefix=/usr/local --default-library=static --buildtype=release && \
#      ninja -C build && ninja -C build install

RUN mkdir -p /usr/local/share/vmaf/models && \
    curl -L -o /usr/local/share/vmaf/models/vmaf_v0.6.1.json \
    https://github.com/Netflix/vmaf/raw/v3.0.0/model/vmaf_v0.6.1.json

RUN curl -L -o /usr/local/share/vmaf/models/vmaf_4k_v0.6.1.json \
    https://github.com/Netflix/vmaf/raw/v3.0.0/model/vmaf_4k_v0.6.1.json

RUN mkdir -p /usr/share/vmaf/models && \
    curl -L -o /usr/share/vmaf/models/vmaf_v0.6.1.json \
    https://github.com/Netflix/vmaf/raw/v3.0.0/model/vmaf_v0.6.1.json

RUN curl -L -o /usr/share/vmaf/models/vmaf_4k_v0.6.1.json \
    https://github.com/Netflix/vmaf/raw/v3.0.0/model/vmaf_4k_v0.6.1.json
    
ENV VMAF_MODEL_PATH=/usr/local/share/vmaf/models/vmaf_v0.6.1.json

# ── opus (audio) ──────────────────────────────────────────────────
RUN git clone --depth 1 -b v1.5.2 https://github.com/xiph/opus.git && \
    cd opus && \
    autoreconf -fis && \
    ./configure --prefix=/usr/local --enable-static --disable-shared --disable-doc --disable-extra-programs && \
    make -j$(nproc) && make install
ENV PKG_CONFIG_PATH="/usr/local/lib/x86_64-linux-gnu/pkgconfig:/usr/local/lib/pkgconfig:$PKG_CONFIG_PATH"
# ── FFmpeg ────────────────────────────────────────────────────────
RUN git clone --depth 1 -b n8.0.1 https://github.com/FFmpeg/FFmpeg.git ffmpeg-src && \
    cd ffmpeg-src && \
    wget -qO- "https://git.ffmpeg.org/gitweb/ffmpeg.git/patch/a5d4c398b411a00ac09d8fe3b66117222323844c" | git apply || true && \
    PKG_CONFIG_PATH="/usr/local/lib/pkgconfig:/usr/local/lib/x86_64-linux-gnu/pkgconfig:/usr/local/lib/aarch64-linux-gnu/pkgconfig" \
    ./configure \
        --prefix=/usr/local \
        --enable-gpl \
        --enable-version3 \
        --enable-static \
        --disable-shared \
        --enable-libx264 \
        --enable-libx265 \
        --enable-libsvtav1 \
        --enable-libvvenc \
        --enable-libdav1d \
        --enable-libvpx \
        --enable-libvmaf \
        --enable-libopus \
        --disable-doc \
        --disable-htmlpages \
        --disable-manpages \
        --disable-podpages \
        --disable-txtpages \
        --extra-cflags="-I/usr/local/include" \
        --extra-ldflags="-L/usr/local/lib -L/usr/local/lib/x86_64-linux-gnu -L/usr/local/lib/aarch64-linux-gnu" \
        --extra-libs="-lpthread -lm -lstdc++" \
        --pkg-config-flags="--static" && \
    make -j$(nproc) && make install

# Verify the build
RUN ffmpeg -version && \
    ffmpeg -encoders 2>/dev/null | grep -E "libx264|libx265|libsvtav1" && \
    ffmpeg -filters 2>/dev/null | grep libvmaf && \
    ffprobe -version

# ── Output stage ──────────────────────────────────────────────────
FROM scratch AS export

ENV VMAF_MODEL_PATH=./usr/local/share/vmaf/models/vmaf_v0.6.1.json
# RUN mkdir -p /usr/local/share/vmaf/models && \
#     curl -L -o /usr/local/share/vmaf/models/vmaf_v0.6.1.json \
#     https://github.com/Netflix/vmaf/raw/v3.0.0/model/vmaf_v0.6.1.json

# RUN curl -L -o /usr/local/share/vmaf/models/vmaf_4k_v0.6.1.json \
#     https://github.com/Netflix/vmaf/raw/v3.0.0/model/vmaf_4k_v0.6.1.json


COPY --from=build /usr/local/bin/ffmpeg /ffmpeg
COPY --from=build /usr/local/bin/ffprobe /ffprobe
COPY --from=build /usr/local/share/vmaf/models/ /usr/local/share/vmaf/models/
COPY --from=build /usr/local/lib/libxevd.so* /usr/local/lib/
#COPY --from=build /usr /usr