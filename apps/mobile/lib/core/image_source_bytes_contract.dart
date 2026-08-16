class ImageSourceBytesException implements Exception {
  const ImageSourceBytesException(this.message);

  final String message;

  @override
  String toString() => message;
}
