class ImageExportException implements Exception {
  const ImageExportException(this.message);

  final String message;

  @override
  String toString() => message;
}

String imageFileStem(String fileName, String extension) {
  final suffix = extension.startsWith('.') ? extension : '.$extension';
  return fileName.toLowerCase().endsWith(suffix.toLowerCase())
      ? fileName.substring(0, fileName.length - suffix.length)
      : fileName;
}

String imageFileName(String fileName, String extension) {
  final suffix = extension.startsWith('.') ? extension : '.$extension';
  return '${imageFileStem(fileName, suffix)}$suffix';
}
